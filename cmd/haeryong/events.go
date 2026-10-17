package main

import (
	"context"

	"haeryong.local/haeryong/internal/adapters/client"
)

func eventCommand(ctx context.Context, opt options, api *client.Client, emit func(any) error) error {
	if len(opt.command) != 1 {
		return invalid("use events [--cursor EPOCH:SEQUENCE]")
	}
	page, err := api.PollEvents(ctx, opt.flags["cursor"])
	if err != nil {
		return productError(err)
	}
	return emit(page)
}
