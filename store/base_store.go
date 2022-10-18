package store

import (
	"fmt"

	pbsubstreams "github.com/streamingfast/substreams/pb/sf/substreams/v1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type baseStore struct {
	Config

	kv          map[string][]byte          // kv is the state, and assumes all deltas were already applied to it.
	deltas      []*pbsubstreams.StoreDelta // deltas are always deltas for the given block.
	lastOrdinal uint64

	logger *zap.Logger
}

func (b *baseStore) Name() string { return b.name }

func (b *baseStore) InitialBlock() uint64 { return b.moduleInitialBlock }

func (b *baseStore) String() string {
	return fmt.Sprintf("%b (%b)", b.name, b.moduleHash)
}

func (b *baseStore) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddString("name", b.name)
	enc.AddString("hash", b.moduleHash)
	enc.AddUint64("module_initial_block", b.moduleInitialBlock)
	enc.AddInt("key_count", len(b.kv))

	return nil
}

func (b *baseStore) Reset() {
	if tracer.Enabled() {
		b.logger.Debug("flushing store", zap.String("name", b.name), zap.Int("delta_count", len(b.deltas)), zap.Int("entry_count", len(b.kv)))
	}
	b.deltas = nil
	b.lastOrdinal = 0
}

func (b *baseStore) bumpOrdinal(ord uint64) {
	if b.lastOrdinal > ord {
		panic("cannot Set or Del a value on a state.Builder with an ordinal lower than the previous")
	}
	b.lastOrdinal = ord
}
