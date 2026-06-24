package diffpane

import "testing"

func TestDecideLoad_UnchangedRawIsSkipped(t *testing.T) {
	// First load of an empty (clean) diff renders the placeholder and reports a hash.
	first := decideLoad([]byte{}, 0)
	if first.unchanged {
		t.Fatal("first load must not be marked unchanged")
	}
	if first.hash == 0 {
		t.Fatal("first load must report a non-zero hash")
	}
	if first.content != "(no uncommitted changes)" {
		t.Fatalf("unexpected content: %q", first.content)
	}

	// Re-loading identical raw bytes against the prior hash is a no-op.
	second := decideLoad([]byte{}, first.hash)
	if !second.unchanged {
		t.Fatal("identical raw bytes must be marked unchanged")
	}

	// Different raw bytes against the prior hash are NOT unchanged.
	third := decideLoad([]byte("something different"), first.hash)
	if third.unchanged {
		t.Fatal("different raw bytes must not be marked unchanged")
	}
}

func TestUpdate_UnchangedPreservesContent(t *testing.T) {
	m := New()
	m, _ = m.Update(LoadedMsg{content: "hello", hash: 42})
	if m.content != "hello" {
		t.Fatalf("expected content set, got %q", m.content)
	}
	m, _ = m.Update(LoadedMsg{unchanged: true})
	if m.content != "hello" {
		t.Fatalf("unchanged msg must preserve content, got %q", m.content)
	}
	m, _ = m.Update(LoadedMsg{content: "world", hash: 43})
	if m.content != "world" {
		t.Fatalf("new content must replace, got %q", m.content)
	}
}
