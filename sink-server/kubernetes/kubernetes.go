package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/streamingfast/shutter"
	pbsql "github.com/streamingfast/substreams-sink-sql/pb/sf/substreams/sink/sql/v1"
	pbsinksvc "github.com/streamingfast/substreams/pb/sf/substreams/sink/service/v1"
	pbsubstreams "github.com/streamingfast/substreams/pb/sf/substreams/v1"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"strings"
	"sync"
	"time"
)

type KubernetesEngine struct {
	shutter.Shutter

	clientSet *kubernetes.Clientset
	namespace string
	apiToken  string //todo: change this to come from user

	logger *zap.Logger

	dbDSNs map[string]string

	resourceMutex sync.Mutex
}

func NewEngine(ctx context.Context, configPath string, namespace string, token string, zlog *zap.Logger) (*KubernetesEngine, error) {
	var config *rest.Config
	var err error
	if configPath == "" {
		config, err = rest.InClusterConfig()
	} else {
		config, err = clientcmd.BuildConfigFromFlags("", configPath)
		if err != nil {
			panic(err.Error())
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err.Error())
	}

	k := &KubernetesEngine{
		clientSet: clientset,
		namespace: namespace,
		apiToken:  token,
		logger:    zlog,
		dbDSNs:    make(map[string]string),
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
				err := k.DeleteExpiredResources(ctx)
				if err != nil {
					k.logger.Error("error deleting expired resources", zap.Error(err))
				}
			}
		}
	}()

	return k, nil
}

func (k *KubernetesEngine) Create(ctx context.Context, deploymentID string, pkg *pbsubstreams.Package, zlog *zap.Logger) error {
	k.resourceMutex.Lock()
	defer k.resourceMutex.Unlock()

	if pkg.SinkConfig.TypeUrl != "sf.substreams.sink.sql.v1.Service" {
		return fmt.Errorf("invalid sinkconfig type: %q. Only sf.substreams.sink.sql.v1.Service is supported for now", pkg.SinkConfig.TypeUrl)
	}

	var k8sCreateFuncs []createFunc

	sinkConfig := &pbsql.Service{}
	if err := pkg.SinkConfig.UnmarshalTo(sinkConfig); err != nil {
		return fmt.Errorf("cannot unmarshal sinkconfig: %w", err)
	}

	switch sinkConfig.GetEngine() {
	case pbsql.Service_unset:
		// nothing to do
	case pbsql.Service_clickhouse:
		return fmt.Errorf("clickhouse engine is not supported yet")
	case pbsql.Service_postgres:
		// create a postgres stateful set
		cf, err := k.newPostgres(ctx, deploymentID, pkg)
		if err != nil {
			return fmt.Errorf("error creating postgres stateful set: %w", err)
		}
		k8sCreateFuncs = append(k8sCreateFuncs, cf)
	}

	scf, err := k.newSink(ctx, deploymentID, "", pkg, sinkConfig)
	if err != nil {
		return fmt.Errorf("error creating sink: %w", err)
	}
	k8sCreateFuncs = append(k8sCreateFuncs, scf)

	if sinkConfig.PgwebFrontend != nil && sinkConfig.PgwebFrontend.Enabled {
		pgwebCreateFunc, err := k.newPGWeb(ctx, deploymentID, "")
		if err != nil {
			return fmt.Errorf("error creating pgweb: %w", err)
		}
		k8sCreateFuncs = append(k8sCreateFuncs, pgwebCreateFunc)
	}

	if sinkConfig.PostgraphileFrontend != nil && sinkConfig.PostgraphileFrontend.Enabled {
		postgraphile, err := k.newPostgraphile(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("error creating postgraphile: %w", err)
		}
		k8sCreateFuncs = append(k8sCreateFuncs, postgraphile)
	}

	createdObjects := make([]*metav1.ObjectMeta, 0)
	for _, f := range k8sCreateFuncs {
		oms, err := f(ctx)
		if err != nil {
			return fmt.Errorf("error creating kubernetes resources: %w", err)
		}

		createdObjects = append(createdObjects, oms...)
	}

	for _, om := range createdObjects {
		zlog.Info("created object", zap.String("name", om.Name))
	}

	return nil
}

func (k *KubernetesEngine) Update(ctx context.Context, deploymentID string, pkg *pbsubstreams.Package, reset bool, zlog *zap.Logger) error {
	return fmt.Errorf("update not implemented for kubernetes engine")
}

func (k *KubernetesEngine) Resume(ctx context.Context, deploymentID string, currentState pbsinksvc.DeploymentStatus, zlog *zap.Logger) (string, error) {
	k.resourceMutex.Lock()
	defer k.resourceMutex.Unlock()

	// Define the name of the StatefulSet
	statefulSetName := "sink-" + deploymentID

	// Get the current scale of the StatefulSet
	sts, err := k.clientSet.AppsV1().StatefulSets(k.namespace).Get(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to get StatefulSet %s: %w", statefulSetName, err)
	}

	// Modify the replicas count
	sts.Spec.Replicas = ref(int32(1))

	// Update the StatefulSet with the new scale
	_, err = k.clientSet.AppsV1().StatefulSets(k.namespace).Update(ctx, sts, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to update StatefulSet %s: %w", statefulSetName, err)
	}

	time.Sleep(10 * time.Second)

	return fmt.Sprintf("deployment %s resumed", deploymentID), nil
}

func (k *KubernetesEngine) Pause(ctx context.Context, deploymentID string, zlog *zap.Logger) (string, error) {
	k.resourceMutex.Lock()
	defer k.resourceMutex.Unlock()

	// Define the name of the StatefulSet
	statefulSetName := "sink-" + deploymentID

	// Get the current scale of the StatefulSet
	sts, err := k.clientSet.AppsV1().StatefulSets(k.namespace).Get(ctx, statefulSetName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to get StatefulSet %s: %w", statefulSetName, err)
	}

	// Modify the replicas count
	sts.Spec.Replicas = ref(int32(0))

	// Update the StatefulSet with the new scale
	_, err = k.clientSet.AppsV1().StatefulSets(k.namespace).Update(ctx, sts, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("unable to update StatefulSet %s: %w", statefulSetName, err)
	}

	time.Sleep(10 * time.Second)

	return fmt.Sprintf("deployment %s paused", deploymentID), nil
}

func (k *KubernetesEngine) Stop(ctx context.Context, deploymentID string, zlog *zap.Logger) (string, error) {
	zlog.Warn("stop not implemented for kubernetes engine. doing a pause instead.")
	return k.Pause(ctx, deploymentID, zlog)
}

func (k *KubernetesEngine) Remove(ctx context.Context, deploymentID string, zlog *zap.Logger) (string, error) {
	labelSelector := fmt.Sprintf("deployment=%s", deploymentID)

	// delete all deployments, stateful sets, configmaps and pvcs for this deployment id

	stsList, err := k.clientSet.AppsV1().StatefulSets(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("error listing statefulsets: %w", err)
	}

	for _, sts := range stsList.Items {
		if err := k.clientSet.AppsV1().StatefulSets(k.namespace).Delete(ctx, sts.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ref(int64(0)),
		}); err != nil {
			return "", fmt.Errorf("error deleting statefulset %q: %w", sts.Name, err)
		}
	}

	deploymentsList, err := k.clientSet.AppsV1().Deployments(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("error listing deployments: %w", err)
	}

	for _, deployment := range deploymentsList.Items {
		if err := k.clientSet.AppsV1().Deployments(k.namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ref(int64(0)),
		}); err != nil {
			return "", fmt.Errorf("error deleting deployment %q: %w", deployment.Name, err)
		}
	}

	pvcsList, err := k.clientSet.CoreV1().PersistentVolumeClaims(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("error listing pvcs: %w", err)
	}

	for _, pvc := range pvcsList.Items {
		if err := k.clientSet.CoreV1().PersistentVolumeClaims(k.namespace).Delete(ctx, pvc.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ref(int64(0)),
		}); err != nil {
			return "", fmt.Errorf("error deleting pvc %q: %w", pvc.Name, err)
		}
	}

	configMapsList, err := k.clientSet.CoreV1().ConfigMaps(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("error listing configmaps: %w", err)
	}

	for _, configMap := range configMapsList.Items {
		if err := k.clientSet.CoreV1().ConfigMaps(k.namespace).Delete(ctx, configMap.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ref(int64(0)),
		}); err != nil {
			return "", fmt.Errorf("error deleting configmap %q: %w", configMap.Name, err)
		}
	}

	svcList, err := k.clientSet.CoreV1().Services(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("error listing services: %w", err)
	}

	for _, svc := range svcList.Items {
		if err := k.clientSet.CoreV1().Services(k.namespace).Delete(ctx, svc.Name, metav1.DeleteOptions{
			GracePeriodSeconds: ref(int64(0)),
		}); err != nil {
			return "", fmt.Errorf("error deleting service %q: %w", svc.Name, err)
		}
	}

	return fmt.Sprintf("deployment %s removed", deploymentID), nil
}

func (k *KubernetesEngine) Info(ctx context.Context, deploymentID string, zlog *zap.Logger) (pbsinksvc.DeploymentStatus, string, map[string]string, *pbsinksvc.PackageInfo, *pbsinksvc.SinkProgress, error) {
	services := map[string]string{}
	var reason string

	var sinkProgress *pbsinksvc.SinkProgress
	blk := k.getProgressBlock(ctx, "sink", deploymentID, zlog)
	if blk != 0 {
		sinkProgress = &pbsinksvc.SinkProgress{
			LastProcessedBlock: blk,
		}
	}

	pkgInfo, err := k.getPackageInfo(ctx, deploymentID)
	if err != nil {
		zlog.Warn("cannot get package info", zap.Error(err))
		return pbsinksvc.DeploymentStatus_UNKNOWN, "", nil, pkgInfo, sinkProgress, nil
	}

	//get sink statefulset.  if this is set to 0 replicas, return PAUSED.
	stslist, err := k.clientSet.AppsV1().StatefulSets(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("deployment=%s", deploymentID),
	})
	if err != nil {
		zlog.Warn("error listing statefulsets", zap.Error(err))
		return pbsinksvc.DeploymentStatus_UNKNOWN, "", nil, pkgInfo, sinkProgress, nil
	}

	var sinkDesiredReplicas int32
	var sinkCurrentReplicas int32
	var paused bool
	var stopped bool
	var pausing bool
	var resuming bool
	var running bool
	for _, sts := range stslist.Items {
		if !strings.HasPrefix(sts.Name, "sink-") {
			if sts.Status.Replicas == 0 && sts.Status.CurrentReplicas == 0 {
				stopped = true
			} else {
				stopped = false
			}
		} else {
			sinkDesiredReplicas = sts.Status.Replicas
			sinkCurrentReplicas = sts.Status.CurrentReplicas

			if sinkDesiredReplicas > sinkCurrentReplicas {
				pausing = true
			} else if sinkDesiredReplicas < sinkCurrentReplicas {
				resuming = true
			} else {
				if sts.Status.Replicas == 0 && sts.Status.CurrentReplicas == 0 {
					paused = true
				} else {
					running = true
				}
			}
		}
	}

	var sinkStatus pbsinksvc.DeploymentStatus

	if stopped {
		sinkStatus = pbsinksvc.DeploymentStatus_STOPPED
	} else if paused {
		sinkStatus = pbsinksvc.DeploymentStatus_PAUSED
	} else if pausing {
		sinkStatus = pbsinksvc.DeploymentStatus_PAUSING
	} else if resuming {
		sinkStatus = pbsinksvc.DeploymentStatus_RESUMING
	} else if running {
		sinkStatus = pbsinksvc.DeploymentStatus_RUNNING
	} else {
		sinkStatus = pbsinksvc.DeploymentStatus_UNKNOWN
	}

	//list all pods for this deployment id.  if any are not in "running" state, return ERROR.  else return RUNNING
	pods, err := k.clientSet.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("deployment=%s", deploymentID),
	})
	if err != nil {
		zlog.Warn("error listing pods", zap.Error(err))
		return pbsinksvc.DeploymentStatus_UNKNOWN, "", nil, pkgInfo, sinkProgress, nil
	}

	status := sinkStatus
	for _, pod := range pods.Items {
		if strings.HasPrefix(pod.Name, "sink-") {
			continue
		}

		if pod.Status.Phase == v1.PodFailed {
			zlog.Info("pod failed", zap.String("pod", pod.Name))
			status = pbsinksvc.DeploymentStatus_FAILING
			reason = fmt.Sprintf("pod %q failed", pod.Name)
			break
		}

		if pod.Status.Phase != v1.PodRunning {
			zlog.Info("pod not running", zap.String("pod", pod.Name))
			status = pbsinksvc.DeploymentStatus_UNKNOWN
			reason = fmt.Sprintf("pod %q not running", pod.Name)
			break
		}
	}

	pods, err = k.clientSet.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("deployment=%s", deploymentID),
	})
	if err != nil {
		zlog.Warn("error listing pods", zap.Error(err))
		return status, "", nil, pkgInfo, sinkProgress, nil
	}
	for _, pod := range pods.Items {
		services[pod.Name] = NewPodStatus(&pod).String()
	}

	return status, reason, services, pkgInfo, sinkProgress, nil
}

func (k *KubernetesEngine) List(ctx context.Context, zlog *zap.Logger) ([]*pbsinksvc.DeploymentWithStatus, error) {
	//get all config maps with label "component=substreams-sink-sql"
	configMaps, err := k.clientSet.CoreV1().ConfigMaps(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "component=substreams-sink-sql",
	})
	if err != nil {
		return nil, fmt.Errorf("error listing configmaps: %w", err)
	}

	deployments := make([]*pbsinksvc.DeploymentWithStatus, 0)
	for _, cm := range configMaps.Items {
		deploymentId, ok := cm.Labels["deployment"]
		if !ok {
			continue
		}

		status, reason, _, info, prog, err := k.Info(ctx, deploymentId, zlog)
		if err != nil {
			return nil, fmt.Errorf("error getting deployment info: %w", err)
		}
		dws := &pbsinksvc.DeploymentWithStatus{
			Id:          deploymentId,
			Status:      status,
			Reason:      reason,
			PackageInfo: info,
			Progress:    prog,
		}
		deployments = append(deployments, dws)
	}

	return deployments, nil
}

func (k *KubernetesEngine) Shutdown(ctx context.Context, zlog *zap.Logger) error {
	// nothing really to do here
	return nil
}

type PodStatus struct {
	Status     string            `json:"status"`
	IP         string            `json:"ip"`
	Containers []ContainerStatus `json:"containers"`
}

type ContainerStatus struct {
	Name  string       `json:"name"`
	Image string       `json:"image"`
	Ports []PortStatus `json:"ports,omitempty"`
}

type PortStatus struct {
	Name     string `json:"name"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

func NewPodStatus(pod *v1.Pod) *PodStatus {
	ps := &PodStatus{
		Status: string(pod.Status.Phase),
		IP:     pod.Status.PodIP,
	}

	//for _, container := range pod.Status.ContainerStatuses {
	//	cs := ContainerStatus{
	//		Name:  container.Name,
	//		Image: container.Image,
	//	}
	//
	//	ps.Containers = append(ps.Containers, cs)
	//}

	for _, container := range pod.Spec.Containers {
		cs := ContainerStatus{
			Name:  container.Name,
			Image: container.Image,
		}

		for _, port := range container.Ports {
			cs.Ports = append(cs.Ports, PortStatus{
				Name:     port.Name,
				Port:     port.ContainerPort,
				Protocol: string(port.Protocol),
			})
		}

		ps.Containers = append(ps.Containers, cs)
	}

	return ps
}

func (ps *PodStatus) String() string {
	b, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return ""
	}

	return string(b)
}
