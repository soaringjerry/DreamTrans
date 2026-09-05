package handlers

import (
	"context"
	"sync"
)

type commandBudget struct{ slots chan struct{} }

func (b *commandBudget) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case b.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			b.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *commandBudget) release() { <-b.slots }

// Lazy initialization happens after configuration is loaded at server startup.
var extractionCommandBudget = sync.OnceValue(func() *commandBudget {
	return &commandBudget{slots: make(chan struct{}, knowledgeExtractWorkerCount())}
})

type derivedUploadBudget struct {
	mu    sync.Mutex
	users map[string]bool
}

var derivedUploads = derivedUploadBudget{users: make(map[string]bool)}

// Admit before decoding large bodies. Do not retain a queue of request bodies
// while OCR is busy; clients can retry 429 responses.
func (b *derivedUploadBudget) acquire(userID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.users[userID] || len(b.users) >= knowledgeExtractWorkerCount() {
		return false
	}
	b.users[userID] = true
	return true
}

func (b *derivedUploadBudget) release(userID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.users, userID)
}
