package main

import "context"

type contextKey string

const contextKeyUserID contextKey = "userID"

func contextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, contextKeyUserID, userID)
}
