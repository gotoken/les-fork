package analysis

import (
	"io"
	"os"
)

func ioReadAll(f *os.File) ([]byte, error) { return io.ReadAll(f) }
