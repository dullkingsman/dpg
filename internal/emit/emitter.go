// Package emit implements pipeline.Emitter. It splits DiffOps into transactional
// and non-transactional groups and wraps them in a Migration.
package emit

import (
	"github.com/dullkingsman/dpg/internal/pipeline"
)

func init() {
	pipeline.Default.Register(pipeline.KeyEmitter, New())
}

// Emitter implements pipeline.Emitter.
type Emitter struct{}

// New returns an Emitter.
func New() *Emitter { return &Emitter{} }

// Emit splits ops into transactional and non-transactional slices.
// Transactional ops are intended to be wrapped in BEGIN/COMMIT by the executor.
// Non-transactional ops (e.g. CREATE INDEX CONCURRENTLY, ALTER TYPE ADD VALUE)
// must run outside any transaction block.
func (e *Emitter) Emit(ops []pipeline.DiffOp, meta pipeline.MigrationMeta) (pipeline.Migration, error) {
	m := pipeline.Migration{Meta: meta}
	for _, o := range ops {
		if o.Transactional() {
			m.Transactional = append(m.Transactional, o)
		} else {
			m.NonTransactional = append(m.NonTransactional, o)
		}
	}
	return m, nil
}

var _ pipeline.Emitter = (*Emitter)(nil)
