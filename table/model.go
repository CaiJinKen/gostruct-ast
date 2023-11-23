package table

import (
	"bytes"
	"sync"
)

type model struct {
	reader, writer *bytes.Buffer
	result         []byte
	once           sync.Once
}

func newModel() *model {
	return &model{
		reader: &bytes.Buffer{},
		writer: &bytes.Buffer{},
	}
}
