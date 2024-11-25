package websocket

import "errors"

type (
	messageHandler func(wsCtx *WSHandlerContext, message map[string]interface{}) error

	messageRouter struct {
		handlers map[Action]messageHandler
	}
)

func NewMessageRouter() *messageRouter {
	return &messageRouter{handlers: make(map[Action]messageHandler)}
}

func (r *messageRouter) RegisterHandler(action Action, handler messageHandler) {
	r.handlers[action] = handler
}

func (r *messageRouter) Route(wsCtx *WSHandlerContext, message WSMessage) error {
	action := message.Action
	handler, ok := r.handlers[action]
	if !ok {
		return errors.New("unsupported action: " + string(action))
	}
	return handler(wsCtx, message.Data)
}
