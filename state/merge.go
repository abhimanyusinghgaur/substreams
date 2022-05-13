package state

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	pbsubstreams "github.com/streamingfast/substreams/pb/sf/substreams/v1"
)

const (
	OutputValueTypeInt64    = "int64"
	OutputValueTypeFloat64  = "float64"
	OutputValueTypeBigInt   = "bigInt"
	OutputValueTypeBigFloat = "bigFloat"
	OutputValueTypeString   = "string"

	mergeValuesKey = "__!__metadata" ///NEVER EVER CHANGE THIS
)

func (b *Builder) writeMergeValues() error {
	mergeInfo := &mergeInfo{
		StoreName:        b.Name,
		UpdatePolicy:     b.updatePolicy,
		ValueType:        b.valueType,
		ModuleHash:       b.ModuleHash,
		ModuleStartBlock: b.ModuleStartBlock,
	}

	data, err := json.Marshal(mergeInfo)
	if err != nil {
		return err
	}

	b.KV[mergeValuesKey] = data

	return nil
}

func (b *Builder) clearMergeValues() {
	delete(b.KV, mergeValuesKey)
}

type mergeInfo struct {
	StoreName        string                                     `json:"store_name,omitempty"`
	UpdatePolicy     pbsubstreams.Module_KindStore_UpdatePolicy `json:"update_policy,omitempty"`
	ValueType        string                                     `json:"value_type,omitempty"`
	ModuleHash       string                                     `json:"module_hash,omitempty"`
	ModuleStartBlock uint64                                     `json:"module_start_block,omitempty"`
}

func (b *Builder) Merge(previous *Builder) error {
	//merge values are not of the correct type for the KV, so we delete them and set them back afterwards.
	b.clearMergeValues()
	defer func() {
		if err := b.writeMergeValues(); err != nil {
			panic(err)
		}
	}()

	next := b

	if next.updatePolicy != previous.updatePolicy {
		return fmt.Errorf("incompatible update policies: policy %q cannot merge policy %q", next.updatePolicy, previous.updatePolicy)
	}

	if next.valueType != previous.valueType {
		return fmt.Errorf("incompatible value types: cannot merge %q and %q", next.valueType, previous.valueType)
	}

	for _, p := range next.DeletedPrefixes {
		previous.DeletePrefix(previous.lastOrdinal, p)
	}

	switch next.updatePolicy {
	case pbsubstreams.Module_KindStore_UPDATE_POLICY_REPLACE:
		for k, v := range previous.KV {
			if _, found := next.KV[k]; !found {
				next.KV[k] = v
			}
		}
	case pbsubstreams.Module_KindStore_UPDATE_POLICY_IGNORE:
		for k, v := range previous.KV {
			next.KV[k] = v
		}
	case pbsubstreams.Module_KindStore_UPDATE_POLICY_SUM:
		// check valueType to do the right thing
		switch next.valueType {
		case OutputValueTypeInt64:
			sum := func(a, b uint64) uint64 {
				return a + b
			}
			for k, v := range previous.KV {
				v0b, fv0 := next.KV[k]
				v0 := foundOrZeroUint64(v0b, fv0)
				v1 := foundOrZeroUint64(v, true)
				next.KV[k] = []byte(fmt.Sprintf("%d", sum(v0, v1)))
			}
		case OutputValueTypeFloat64:
			sum := func(a, b float64) float64 {
				return a + b
			}
			for k, v := range previous.KV {
				v0b, fv0 := next.KV[k]
				v0 := foundOrZeroFloat(v0b, fv0)
				v1 := foundOrZeroFloat(v, true)
				next.KV[k] = []byte(floatToStr(sum(v0, v1)))
			}
		case OutputValueTypeBigInt:
			sum := func(a, b *big.Int) *big.Int {
				return bi().Add(a, b)
			}
			for k, v := range previous.KV {
				v0b, fv0 := next.KV[k]
				v0 := foundOrZeroBigInt(v0b, fv0)
				v1 := foundOrZeroBigInt(v, true)
				next.KV[k] = []byte(fmt.Sprintf("%d", sum(v0, v1)))
			}
		case OutputValueTypeBigFloat:
			sum := func(a, b *big.Float) *big.Float {
				return bf().Add(a, b).SetPrec(100)
			}
			for k, v := range previous.KV {
				v0b, fv0 := next.KV[k]
				v0 := foundOrZeroBigFloat(v0b, fv0)
				v1 := foundOrZeroBigFloat(v, true)
				next.KV[k] = []byte(bigFloatToStr(sum(v0, v1)))
			}
		default:
			return fmt.Errorf("update policy %q not supported for value type %s", next.updatePolicy, next.valueType)
		}
	case pbsubstreams.Module_KindStore_UPDATE_POLICY_MAX:
		switch next.valueType {
		case OutputValueTypeInt64:
			max := func(a, b uint64) uint64 {
				if a >= b {
					return a
				}
				return b
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroUint64(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(fmt.Sprintf("%d", v1))
					continue
				}
				v0 := foundOrZeroUint64(v, true)

				next.KV[k] = []byte(fmt.Sprintf("%d", max(v0, v1)))
			}
		case OutputValueTypeFloat64:
			min := func(a, b float64) float64 {
				if a < b {
					return b
				}
				return a
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroFloat(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(floatToStr(v1))
					continue
				}
				v0 := foundOrZeroFloat(v, true)

				next.KV[k] = []byte(floatToStr(min(v0, v1)))
			}
		case OutputValueTypeBigInt:
			max := func(a, b *big.Int) *big.Int {
				if a.Cmp(b) <= 0 {
					return b
				}
				return a
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroBigInt(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(v1.String())
					continue
				}
				v0 := foundOrZeroBigInt(v, true)

				next.KV[k] = []byte(fmt.Sprintf("%d", max(v0, v1)))
			}
		case OutputValueTypeBigFloat:
			max := func(a, b *big.Float) *big.Float {
				if a.Cmp(b) <= 0 {
					return b
				}
				return a
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroBigFloat(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(bigFloatToStr(v1))
					continue
				}
				v0 := foundOrZeroBigFloat(v, true)

				next.KV[k] = []byte(bigFloatToStr(max(v0, v1)))
			}
		default:
			return fmt.Errorf("update policy %q not supported for value type %s", next.updatePolicy, next.valueType)
		}
	case pbsubstreams.Module_KindStore_UPDATE_POLICY_MIN:
		switch next.valueType {
		case OutputValueTypeInt64:
			min := func(a, b uint64) uint64 {
				if a <= b {
					return a
				}
				return b
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroUint64(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(fmt.Sprintf("%d", v1))
					continue
				}
				v0 := foundOrZeroUint64(v, true)

				next.KV[k] = []byte(fmt.Sprintf("%d", min(v0, v1)))
			}
		case OutputValueTypeFloat64:
			min := func(a, b float64) float64 {
				if a < b {
					return a
				}
				return b
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroFloat(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(floatToStr(v1))
					continue
				}
				v0 := foundOrZeroFloat(v, true)

				next.KV[k] = []byte(floatToStr(min(v0, v1)))
			}
		case OutputValueTypeBigInt:
			min := func(a, b *big.Int) *big.Int {
				if a.Cmp(b) <= 0 {
					return a
				}
				return b
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroBigInt(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(v1.String())
					continue
				}
				v0 := foundOrZeroBigInt(v, true)

				next.KV[k] = []byte(fmt.Sprintf("%d", min(v0, v1)))
			}
		case OutputValueTypeBigFloat:
			min := func(a, b *big.Float) *big.Float {
				if a.Cmp(b) <= 0 {
					return a
				}
				return b
			}
			for k, v := range previous.KV {
				v1 := foundOrZeroBigFloat(v, true)
				v, found := next.KV[k]
				if !found {
					next.KV[k] = []byte(bigFloatToStr(v1))
					continue
				}
				v0 := foundOrZeroBigFloat(v, true)

				next.KV[k] = []byte(bigFloatToStr(min(v0, v1)))
			}
		default:
			return fmt.Errorf("update policy %q not supported for value type %s", next.updatePolicy, next.valueType)
		}
	default:
		return fmt.Errorf("update policy %q not supported", next.updatePolicy) // should have been validated already
	}

	next.partialMode = previous.partialMode
	if next.partialMode {
		next.BlockRange.StartBlock = previous.BlockRange.StartBlock
	}

	return nil
}

func foundOrZeroUint64(in []byte, found bool) uint64 {
	if !found {
		return 0
	}
	val, err := strconv.ParseInt(string(in), 10, 64)
	if err != nil {
		return 0
	}
	return uint64(val)
}

func foundOrZeroBigFloat(in []byte, found bool) *big.Float {
	if !found {
		return bf()
	}
	return bytesToBigFloat(in)
}

func foundOrZeroBigInt(in []byte, found bool) *big.Int {
	if !found {
		return bi()
	}
	return bytesToBigInt(in)
}

func foundOrZeroFloat(in []byte, found bool) float64 {
	if !found {
		return float64(0)
	}

	f, err := strconv.ParseFloat(string(in), 64)
	if err != nil {
		return float64(0)
	}
	return f
}

func strToBigFloat(in string) *big.Float {
	newFloat, _, err := big.ParseFloat(in, 10, 100, big.ToNearestEven)
	if err != nil {
		panic(fmt.Sprintf("cannot load float %q: %s", in, err))
	}
	return newFloat.SetPrec(100)
}

func strToFloat(in string) float64 {
	newFloat, _, err := big.ParseFloat(in, 10, 100, big.ToNearestEven)
	if err != nil {
		panic(fmt.Sprintf("cannot load float %q: %s", in, err))
	}
	f, _ := newFloat.SetPrec(100).Float64()
	return f
}

func strToBigInt(in string) *big.Int {
	i64, err := strconv.ParseInt(in, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("cannot load int %q: %s", in, err))
	}
	return big.NewInt(i64)
}

func bytesToBigFloat(in []byte) *big.Float {
	return strToBigFloat(string(in))
}

func bytesToBigInt(in []byte) *big.Int {
	return strToBigInt(string(in))
}

func floatToStr(f float64) string {
	return big.NewFloat(f).Text('g', -1)
}

func floatToBytes(f float64) []byte {
	return []byte(floatToStr(f))
}

func intToBytes(i int) []byte {
	return []byte(strconv.Itoa(i))
}

func bytesToInt(b []byte) int {
	i, err := strconv.Atoi(string(b))
	if err != nil {
		panic(fmt.Sprintf("cannot convert string %s to int: %s", string(b), err.Error()))
	}
	return i
}

func bigFloatToStr(f *big.Float) string {
	return f.Text('g', -1)
}

func bigFloatToBytes(f *big.Float) []byte {
	return []byte(bigFloatToStr(f))
}

var bf = func() *big.Float { return new(big.Float).SetPrec(100) }
var bi = func() *big.Int { return new(big.Int) }
