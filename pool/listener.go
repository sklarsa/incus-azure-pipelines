package pool

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lxc/incus/v6/shared/api"
)

func (p *Pool) ListenForDeletes(ctx context.Context, agentsToCreate chan<- int) error {

	handlerFunc := func(e api.Event) {
		meta := map[string]any{}
		if err := json.Unmarshal(e.Metadata, &meta); err != nil {
			p.logger.Error("error unmarshaling event", "err", err, "meta", e.Metadata)
			return
		}

		if meta["level"] == "info" &&
			meta["message"] == "Deleted instance" {

			context, ok := meta["context"].(map[string]any)
			if !ok {
				p.logger.Warn("unexpected event format, no 'context' map found", "data", e)
				return
			}

			instance, ok := context["instance"].(string)
			if !ok {
				p.logger.Error("unexpected event format, context.instance is not a string", "data", e)
				return
			}

			idx, err := p.agentIndex(instance)
			if err != nil {
				return
			}
			p.logger.Info("container deleted", "name", instance)
			agentsToCreate <- idx
		}
	}

	l, err := p.c.GetEvents()
	if err != nil {
		return fmt.Errorf("error setting up incus event listener: %w", err)
	}
	go func() {
		<-ctx.Done()
		l.Disconnect()
	}()
	defer l.Disconnect()

	h, err := l.AddHandler(nil, handlerFunc)
	if err != nil {
		return err
	}
	defer func() { _ = l.RemoveHandler(h) }()

	return l.Wait()

}
