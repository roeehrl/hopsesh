package ir

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLimitsNormalizeAndCheck(t *testing.T) {
	d := DefaultLimits()
	if d.ReadBytes != MaxTranscriptBytes || d.RecordBytes != 32<<20 || d.NativeFileBytes != 1<<30 || d.Older != OlderExtract || d.ContextBudget != 0 {
		t.Fatalf("defaults changed: %+v", d)
	}
	l := Limits{ReadBytes: 64 << 30, ContextBudget: 10, Older: "bogus"}.Normalize()
	if l.ReadBytes != CeilingReadBytes || l.ContextBudget != MinContextBudget || l.Older != OlderExtract {
		t.Fatalf("not clamped: %+v", l)
	}
	for _, bad := range []Limits{{ReadBytes: -1}, {ArchiveBytes: CeilingArchiveBytes + 1}, {ContextBudget: 100}, {Older: "all"}} {
		if bad.Check() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if (Limits{ContextBudget: 8000}).Allowance(20000) != 8000 || (Limits{ContextBudget: 8000}).Allowance(5000) != 5000 || (Limits{}).Allowance(5000) != 5000 {
		t.Fatal("a user budget must only lower the model allowance")
	}
}

func TestLimitsTravelWithTheOperation(t *testing.T) {
	ctx := WithLimits(context.Background(), Limits{ReadBytes: 1 << 20})
	if LimitsFrom(ctx).ReadBytes != 1<<20 || LimitsFrom(context.Background()).ReadBytes != DefaultReadBytes {
		t.Fatal("limits leaked between operations")
	}
}

func TestBoundedReaderUsesOperationLimitAndNamesSetting(t *testing.T) {
	ctx := WithLimits(context.Background(), Limits{ReadBytes: 1 << 20})
	_, err := io.ReadAll(&BoundedReader{Context: ctx, Reader: strings.NewReader(strings.Repeat("x", 2<<20))})
	le, ok := AsLimit(err)
	if !ok || !errors.Is(err, ErrLimit) || le.Stage != StageRead || le.Limit != 1<<20 || le.Setting() != "history.read_memory_mb" {
		t.Fatalf("want a read LimitError, got %v", err)
	}
	if !strings.Contains(err.Error(), "history.read_memory_mb") {
		t.Fatalf("error does not name the setting: %v", err)
	}
	if b, err := io.ReadAll(&BoundedReader{Context: ctx, Reader: strings.NewReader(strings.Repeat("x", 1<<20))}); err != nil || len(b) != 1<<20 {
		t.Fatalf("exactly the limit must read: %d %v", len(b), err)
	}
}
