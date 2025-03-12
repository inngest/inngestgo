package internal

import "context"

type eventSenderCtxKeyType struct{}

var eventSenderCtxKey = eventSenderCtxKeyType{}

type eventSender interface {
	Send(ctx context.Context, evt any) (string, error)
	SendMany(ctx context.Context, evt []any) ([]string, error)
}

func ContextWithEventSender(ctx context.Context, sender eventSender) context.Context {
	return context.WithValue(ctx, eventSenderCtxKey, sender)
}

func EventSenderFromContext(ctx context.Context) (eventSender, bool) {
	sender, ok := ctx.Value(eventSenderCtxKey).(eventSender)
	return sender, ok
}
