package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/jadmadi/sila/internal/goals"
)

func (gc *goalsContext) handlePacket(targetDir string, args []string) {
	jsonOut := gc.jsonOut
	var positional []string

	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonOut = true
		} else if strings.HasPrefix(a, "-") {
			if jsonOut {
				emitErrorEnvelope("USAGE", fmt.Sprintf("unknown flag: %s", a), "sila goals packet <slug> <task-id> [--json]")
				os.Exit(2)
			}
			fmt.Fprintf(os.Stderr, "unknown flag: %s\nUsage: sila goals packet <slug> <task-id> [--json]\n", a)
			os.Exit(2)
		} else {
			positional = append(positional, a)
		}
	}

	if len(positional) < 2 {
		if jsonOut {
			emitErrorEnvelope("USAGE", "goal slug and task id are required", "sila goals packet <slug> <task-id> [--json]")
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "Usage: sila goals packet <slug> <task-id> [--json]")
		os.Exit(2)
	}

	slug := positional[0]
	taskID := positional[1]

	if targetDir == "" {
		targetDir = goals.ResolveGoalsRootDir(".")
	}

	packet, err := goals.BuildTaskPacket(targetDir, slug, taskID)
	if err != nil {
		if jsonOut {
			emitErrorEnvelope("TASK_PACKET_BUILD_FAILED", err.Error(), "sila goals readiness "+slug)
		} else {
			fmt.Fprintf(os.Stderr, "Error building task packet: %v\n", err)
		}
		os.Exit(1)
	}

	if jsonOut {
		data, err := goals.EncodeTaskPacketJSON(packet)
		if err != nil {
			emitErrorEnvelope("TASK_PACKET_ENCODE_FAILED", err.Error(), "sila goals packet "+slug+" "+taskID)
			os.Exit(1)
		}
		if outModes.envelope {
			var payload any
			if err := json.Unmarshal(data, &payload); err != nil {
				emitErrorEnvelope("TASK_PACKET_ENCODE_FAILED", err.Error(), "sila goals packet "+slug+" "+taskID)
				os.Exit(1)
			}
			emitEnvelope(payload, nil, nil)
		} else {
			fmt.Println(string(data))
		}
	} else {
		md, err := goals.RenderTaskPacketMarkdown(packet)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error rendering task packet: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(md)
	}

	if !packet.Executable {
		os.Exit(1)
	}
	os.Exit(0)
}
