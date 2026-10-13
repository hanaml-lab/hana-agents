// agentctl talks to runtimed.
//
//	agentctl jobs start plan.json
//	agentctl jobs list
//	agentctl jobs tail <id>                  watch steps as they are journalled
//	agentctl jobs replay <id>                rebuild state from the journal, without calling any tool
//	agentctl jobs replay <id> -journal dir   the same, from a journal directory with no server
//	agentctl jobs approve <id> -as <name>
//	agentctl jobs reject <id> -as <name> -reason "payee changed by email"
//
// The server address comes from -server or AGENT_RUNTIME (default http://localhost:7070).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/runtime"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: agentctl jobs start <plan.json> | list | tail <id> | replay <id> [-journal dir] | approve <id> -as <name> | reject <id> -as <name> -reason <text>")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 3 || os.Args[1] != "jobs" {
		usage()
	}
	cmd, rest := os.Args[2], os.Args[3:]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	server := fs.String("server", envOr("AGENT_RUNTIME", "http://localhost:7070"), "runtimed address")
	journalDir := fs.String("journal", "", "read a journal directory directly (replay only)")
	as := fs.String("as", "", "approver name (approve and reject)")
	reason := fs.String("reason", "", "why the call is refused (reject only)")
	var id string
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		id, rest = rest[0], rest[1:]
	}
	_ = fs.Parse(rest)
	base := strings.TrimRight(*server, "/")
	var err error
	switch cmd {
	case "start":
		err = start(base, id)
	case "list":
		err = get(base+"/jobs", os.Stdout)
	case "tail":
		err = tail(base, id)
	case "replay":
		err = replay(base, id, *journalDir)
	case "approve":
		if *as == "" {
			usage()
		}
		err = post(base+"/jobs/"+id+"/approve", map[string]string{"approver": *as}, os.Stdout)
	case "reject":
		if *as == "" || *reason == "" {
			usage()
		}
		err = post(base+"/jobs/"+id+"/reject", map[string]string{"approver": *as, "reason": *reason}, os.Stdout)
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentctl:", err)
		os.Exit(1)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func get(url string, w io.Writer) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(b))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func post(url string, body any, w io.Writer) error {
	b, _ := json.Marshal(body)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func start(base, planPath string) error {
	b, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	var job map[string]any
	if err := json.Unmarshal(b, &job); err != nil {
		return err
	}
	return post(base+"/jobs", job, os.Stdout)
}

func tail(base, id string) error {
	var after int64
	for {
		var buf bytes.Buffer
		if err := get(fmt.Sprintf("%s/jobs/%s/events?after=%d", base, id, after), &buf); err != nil {
			return err
		}
		var evs []runtime.Event
		if err := json.Unmarshal(buf.Bytes(), &evs); err != nil {
			return err
		}
		for _, ev := range evs {
			after = ev.Seq
			fmt.Printf("%4d %s %-16s %-12s %s\n", ev.Seq, ev.At.Format("15:04:05"), ev.Kind, ev.StepID, short(ev.Payload))
			if ev.Kind == journal.JobDone || ev.Kind == journal.JobFailed || ev.Kind == journal.ApprovalNeeded {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func short(p json.RawMessage) string {
	s := string(p)
	if len(s) > 100 {
		return s[:97] + "..."
	}
	return s
}

func replay(base, id, dir string) error {
	var evs []runtime.Event
	if dir != "" {
		j, err := journal.NewFile(dir)
		if err != nil {
			return err
		}
		if evs, err = j.Load(context.Background(), id); err != nil {
			return err
		}
	} else {
		var buf bytes.Buffer
		if err := get(base+"/jobs/"+id+"/events", &buf); err != nil {
			return err
		}
		if err := json.Unmarshal(buf.Bytes(), &evs); err != nil {
			return err
		}
	}
	st := runtime.Fold(id, evs)
	fmt.Printf("job %s: %s after %d events\n", id, st.Status, st.Events)
	for _, s := range st.Plan {
		mark := "pending"
		if _, ok := st.Results[s.ID]; ok {
			mark = "done"
		}
		fmt.Printf("  %-14s %-18s attempt %d  %s\n", s.ID, s.Tool, st.Attempt[s.ID], mark)
	}
	if st.Pending != nil {
		fmt.Printf("  waiting for approval of %s (rule %s)\n", st.Pending.Action, st.Pending.Rule)
	}
	if st.Error != "" {
		fmt.Printf("  error: %s\n", st.Error)
	}
	return nil
}
