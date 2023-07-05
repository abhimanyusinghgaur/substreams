package stage

// This means that this single Store has completed its full sync, up to the target block
type MsgAllStoresCompleted struct {
	Unit
}

type MsgMergeFinished struct {
	Unit
} // A single partial store was successfully merged into the full store.

type MsgMergeFailed struct {
	Unit
	Error error
}
