package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/render"
	"github.com/jadmadi/sila/internal/store"
)

// asOfRequest carries the --as-of flag for this invocation.
var asOfRequest string

type asOfOutput struct {
	AsOf       string               `json:"as_of"`
	Historical bool                 `json:"historical"`
	Clamped    bool                 `json:"clamped,omitempty"`
	Facts      []store.Fact         `json:"facts,omitempty"`
	Records    []store.MemoryRecord `json:"records,omitempty"`
}

func historicalSnapshot(dbPath, project string, raw string) (*asOfOutput, error) {
	when, clamped, err := store.ParseAsOf(raw, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	st, err := store.OpenRO(dbPath)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	facts, err := st.FactsVisibleAt(project, when, 200)
	if err != nil {
		return nil, err
	}
	records, err := st.MemoryRecordsVisibleAt(project, when, 200)
	if err != nil {
		return nil, err
	}
	return &asOfOutput{AsOf: when.Format(time.RFC3339), Historical: true, Clamped: clamped, Facts: facts, Records: records}, nil
}

func runAsOfOutput(ui render.UI, dbPath, project string, jsonOut bool, withRecords bool) {
	snapshot, err := historicalSnapshot(dbPath, project, asOfRequest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sila: %v\n", err)
		os.Exit(2)
	}
	if !withRecords {
		snapshot.Records = nil
	}
	if jsonOut {
		emitJSON(snapshot)
		return
	}
	fmt.Fprintf(os.Stdout, "as of %s (historical%s)\n", snapshot.AsOf, map[bool]string{true: ", clamped", false: ""}[snapshot.Clamped])
	rows := make([][]string, 0, len(snapshot.Facts)+len(snapshot.Records))
	for _, fact := range snapshot.Facts {
		rows = append(rows, []string{"fact", fact.ID, fact.Kind, fact.Claim})
	}
	for _, record := range snapshot.Records {
		rows = append(rows, []string{"memo", record.ID, record.Kind, record.Summary})
	}
	if len(rows) == 0 {
		fmt.Fprintln(os.Stdout, "no rows visible at that time")
		return
	}
	fmt.Fprint(os.Stdout, ui.Table([]string{"kind", "id", "type", "summary"}, rows, []string{"left", "left", "left", "left"}))
}

func asOfEnabled() bool { return strings.TrimSpace(asOfRequest) != "" }
