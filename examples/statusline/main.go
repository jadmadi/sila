// Command statusline prints one line per daemon write event without polling.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jadmadi/sila/internal/daemon"
)

func main() {
	socket := daemon.DefaultSocketPath()
	if len(os.Args) > 1 {
		socket = os.Args[1]
	}
	client, err := daemon.Dial(socket, time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "statusline: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	err = client.Subscribe(ctx, []string{"write"}, func(event daemon.Event) error {
		encoded, err := json.Marshal(event)
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "statusline: %v\n", err)
		os.Exit(1)
	}
}
