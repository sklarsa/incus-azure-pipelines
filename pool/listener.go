package pool

import (
	"encoding/json"
	"fmt"
	"sync"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

type Listener struct {
	p *Pool
	l *incus.EventListener
	h *incus.EventTarget

	closeFunc *sync.Once
}

func NewListener(p *Pool, agentsToCreate chan<- int) (*Listener, error) {
	l := &Listener{
		p:         p,
		closeFunc: &sync.Once{},
	}

	var err error
	l.l, err = p.c.GetEvents()
	if err != nil {
		return nil, fmt.Errorf("error setting up incus event listener: %w", err)
	}

	l.h, err = l.l.AddHandler(nil, func(e api.Event) {

		meta := map[string]any{}
		if err := json.Unmarshal(e.Metadata, &meta); err != nil {
			l.p.logger.Error("error unmarshaling event", "err", err, "meta", e.Metadata)
			return
		}

		if meta["level"] == "info" &&
			meta["message"] == "Deleted instance" {

			context, ok := meta["context"].(map[string]any)
			if !ok {
				l.p.logger.Warn("unexpected event format, no 'context' map found", "data", e)
				return
			}

			instance, ok := context["instance"].(string)
			if !ok {
				l.p.logger.Error("unexpected event format, context.instance is not a string", "data", e)
				return
			}

			idx := p.AgentIndex(instance)
			if idx >= 0 {
				l.p.logger.Info("container deleted", "name", instance)
				agentsToCreate <- idx
			}
		}

	})

	return l, err
}

func (l *Listener) Close() {
	l.closeFunc.Do(func() {
		if l.l == nil {
			return
		}
		defer l.l.Disconnect()

		if l.h != nil {
			if err := l.l.RemoveHandler(l.h); err != nil {
				l.p.logger.Error("error removing event handler", "err", err)
			}
		}
	})

}
