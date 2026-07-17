// runtimed runs agent jobs durably and serves the job API.
//
//	runtimed -journal ./journal -policies ../policy-engine/policies/examples -tools http://localhost:7071
//
// Jobs left running by a previous process are resumed from the journal at start-up.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/journal"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/otel"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/planner"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/internal/worker"
	"github.com/hanaml-lab/hana-agents/go/agent-runtime/runtime"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

func main() {
	var toolServers list
	addr := flag.String("addr", ":7070", "listen address")
	journalArg := flag.String("journal", "journal", "journal directory, or a postgres:// DSN")
	policies := flag.String("policies", "", "directory of .cedar policies (required unless -allow-all)")
	allowAll := flag.Bool("allow-all", false, "run without policies; for local development only")
	flag.Var(&toolServers, "tools", "base URL of a tool server; repeatable")
	plannerURL := flag.String("planner-endpoint", "", "OpenAI-compatible endpoint for planning jobs without a plan")
	plannerModel := flag.String("planner-model", "Qwen/Qwen3-32B", "planner model")
	concurrency := flag.Int("concurrency", 16, "jobs run at once")
	lease := flag.Duration("lease", 90*time.Second, "lease on a job before another worker may take it")
	heartbeat := flag.Duration("heartbeat", 15*time.Second, "lease renewal interval")
	traces := flag.String("traces", "", "write step spans as JSON lines to this file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var j runtime.Journal
	var err error
	if strings.HasPrefix(*journalArg, "postgres://") || strings.HasPrefix(*journalArg, "postgresql://") {
		j, err = journal.OpenPostgres(ctx, *journalArg)
	} else {
		j, err = journal.NewFile(*journalArg)
	}
	if err != nil {
		log.Fatal(err)
	}

	var pol runtime.Policy
	switch {
	case *policies != "":
		engine, err := decide.Load(*policies)
		if err != nil {
			log.Fatal(err)
		}
		pol = engine
		log.Printf("policy version %s", engine.Version())
	case *allowAll:
		pol = runtime.AllowAll{}
		log.Print("warning: running without policies")
	default:
		log.Fatal("give -policies, or -allow-all for local development")
	}

	var pl runtime.Planner = planner.Static{}
	if *plannerURL != "" {
		pl = planner.Chat{Endpoint: *plannerURL, Model: *plannerModel}
	}
	engine := runtime.NewEngine(j, pl, pol)
	if *traces != "" {
		f, err := os.OpenFile(*traces, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		engine.Tracer = otel.New(f)
	}
	for _, base := range toolServers {
		ts, err := runtime.Discover(ctx, base)
		if err != nil {
			log.Fatalf("tool server %s: %v", base, err)
		}
		engine.Register(ts...)
		log.Printf("%d tools from %s", len(ts), base)
	}

	q := worker.NewMem()
	pool := &worker.Pool{Queue: q, Concurrency: *concurrency, LeaseTTL: *lease, Heartbeat: *heartbeat,
		Run: func(ctx context.Context, t worker.Task) error {
			_, err := engine.Run(ctx, t.JobID)
			if err == nil || errors.Is(err, runtime.ErrAwaitingApproval) || errors.Is(err, runtime.ErrJobFailed) {
				return nil
			}
			return err
		},
		OnError: func(t worker.Task, err error) {
			log.Printf("job %s: %v (will retry when the lease expires)", t.JobID, err)
		},
	}
	go pool.Serve(ctx)

	ids, err := j.Jobs(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, id := range ids {
		if st, err := engine.State(ctx, id); err == nil && st.Status == runtime.StatusRunning {
			_ = q.Put(ctx, worker.Task{JobID: id})
			log.Printf("resuming job %s", id)
		}
	}

	srv := &http.Server{Addr: *addr, Handler: routes(engine, q)}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
	}()
	log.Printf("runtimed listening on %s", *addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

type jobRequest struct {
	Goal       string         `json:"goal"`
	Principal  string         `json:"principal"`
	Tools      []string       `json:"tools"`
	Plan       []runtime.Step `json:"plan"`
	Budget     runtime.Budget `json:"budget"`
	Context    map[string]any `json:"context"`
	MaxReturns int            `json:"max_returns_per_step"`
}

type jobStatus struct {
	ID        string                   `json:"id"`
	Status    runtime.Status           `json:"status"`
	Goal      string                   `json:"goal"`
	StepsDone int                      `json:"steps_done"`
	StepsUsed int                      `json:"steps_used"`
	Tokens    int                      `json:"tokens"`
	Pending   *runtime.PendingApproval `json:"pending_approval,omitempty"`
	Error     string                   `json:"error,omitempty"`
	Result    json.RawMessage          `json:"result,omitempty"`
}

func status(st runtime.State) jobStatus {
	out := jobStatus{ID: st.ID, Status: st.Status, Goal: st.Spec.Goal, StepsDone: len(st.Results),
		StepsUsed: st.StepsUsed, Tokens: st.Tokens, Pending: st.Pending, Error: st.Error}
	if st.Status == runtime.StatusDone && len(st.Plan) > 0 {
		out.Result = st.Results[st.Plan[len(st.Plan)-1].ID]
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func routes(e *runtime.Engine, q worker.Queue) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", func(w http.ResponseWriter, r *http.Request) {
		var req jobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		job := runtime.Job{Goal: req.Goal, Principal: req.Principal, Plan: req.Plan, Budget: req.Budget,
			Context: req.Context, MaxReturns: req.MaxReturns}
		id, err := e.Start(r.Context(), job)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = q.Put(r.Context(), worker.Task{JobID: id})
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /jobs", func(w http.ResponseWriter, r *http.Request) {
		ids, err := e.Journal.Jobs(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out := []jobStatus{}
		for _, id := range ids {
			if st, err := e.State(r.Context(), id); err == nil {
				out = append(out, status(st))
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		st, err := e.State(r.Context(), r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status(st))
	})
	mux.HandleFunc("GET /jobs/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		evs, err := e.Journal.Load(r.Context(), r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		out := []runtime.Event{}
		for _, ev := range evs {
			if ev.Seq > after {
				out = append(out, ev)
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /jobs/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Approver string `json:"approver"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Approver == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approver is required"})
			return
		}
		id := r.PathValue("id")
		if err := e.Approve(r.Context(), id, body.Approver); err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		_ = q.Put(r.Context(), worker.Task{JobID: id})
		writeJSON(w, http.StatusOK, map[string]string{"id": id, "approved_by": body.Approver})
	})
	return mux
}
