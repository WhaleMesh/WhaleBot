package main

import (
	"strconv"
	"strings"
	"testing"
)

func TestNewSessionGeneratesUniqueIDs(t *testing.T) {
	mgr := newConversationManager()
	chatID := int64(123456789)

	first := mgr.newSession(chatID)
	second := mgr.newSession(chatID)

	if first == second {
		t.Fatalf("expected unique session ids, got same id: %s", first)
	}

	prefix := strconv.FormatInt(chatID, 10) + "-"
	if !strings.HasPrefix(first, prefix) || !strings.HasPrefix(second, prefix) {
		t.Fatalf("expected both session ids to use prefix %q, got %q and %q", prefix, first, second)
	}
}

func TestNewSessionNotBaseChatIDAfterRestart(t *testing.T) {
	chatID := int64(555)
	baseID := strconv.FormatInt(chatID, 10)

	mgr := newConversationManager()
	created := mgr.newSession(chatID)
	if created == baseID {
		t.Fatalf("expected /new id to differ from base chat id %q", baseID)
	}

	// Simulate process restart by creating a fresh in-memory manager.
	restartedMgr := newConversationManager()
	afterRestart := restartedMgr.newSession(chatID)
	if afterRestart == baseID {
		t.Fatalf("expected /new id after restart to differ from base chat id %q", baseID)
	}
}
