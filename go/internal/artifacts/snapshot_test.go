package artifacts

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/dvmrry/infrawright-dev/go/internal/procerr"
)

type partialSnapshotWriter struct {
	maximum int
	content []byte
}

func (w *partialSnapshotWriter) Write(value []byte) (int, error) {
	length := min(len(value), w.maximum)
	w.content = append(w.content, value[:length]...)
	if length < len(value) {
		return length, io.ErrShortWrite
	}
	return length, nil
}

type zeroSnapshotWriter struct{}

func (zeroSnapshotWriter) Write([]byte) (int, error) {
	return 0, nil
}

type failedSnapshotWriter struct {
	err error
}

func (w failedSnapshotWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestWriteAllCompletesPartialWrites(t *testing.T) {
	writer := &partialSnapshotWriter{maximum: 2}
	content := []byte("partial writes")
	if err := writeAll(writer, content); err != nil {
		t.Fatalf("writeAll(partial writer, %q) error = %v, want nil", content, err)
	}
	if !bytes.Equal(writer.content, content) {
		t.Errorf("writeAll(partial writer) content = %q, want %q", writer.content, content)
	}
}

func TestWriteAllRejectsZeroProgress(t *testing.T) {
	failure := requireFailure(
		t,
		writeAll(zeroSnapshotWriter{}, []byte("content")),
		"SNAPSHOT_FAILED",
		procerr.CategoryIO,
	)
	if failure.Message != "unable to write plan snapshot" {
		t.Errorf("writeAll(zero writer) message = %q, want %q", failure.Message, "unable to write plan snapshot")
	}
}

func TestWriteAllPreservesRawWriteFailure(t *testing.T) {
	want := errors.New("write failure")
	if got := writeAll(failedSnapshotWriter{err: want}, []byte("content")); !errors.Is(got, want) {
		t.Errorf("writeAll(failed writer) error = %v, want errors.Is(error, %v)", got, want)
	}
}
