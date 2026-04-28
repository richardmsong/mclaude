package main

import "context"

type contextKey string

const contextKeyUserID contextKey = "userID"
const contextKeyUserSlug contextKey = "userSlug"

func contextWithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, contextKeyUserID, userID)
}

func contextWithUserSlug(ctx context.Context, userSlug string) context.Context {
	return context.WithValue(ctx, contextKeyUserSlug, userSlug)
}
