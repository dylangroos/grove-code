package termpane

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestStartCapturesOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := Start(ctx, Spec{
		Command: []string{"sh", "-c", "echo grove-hello; sleep 0.1"},
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	// Give the shell a moment to write output.
	time.Sleep(300 * time.Millisecond)
	out := h.Render()
	if !strings.Contains(out, "grove-hello") {
		t.Fatalf("emulator did not capture output; got: %q", out)
	}
	if err := h.Wait(); err != nil {
		// exit code 0 is success; anything else we ignore for this smoke.
		_ = err
	}
}
