// policyd serves policy decisions over HTTP.
//
//	policyd -policies policies/ -addr :8181 -audit audit.jsonl
//
//	POST /v1/decide     {"principal","action","args","context","token"} -> decision
//	POST /v1/approvals  {"principal","action","args","approver","ttl_seconds"} -> {"digest"}
//
// A held request carries "approval_digest"; once a person approves that digest, the same
// request with the same arguments is allowed exactly once.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/approval"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/audit"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/capability"
	"github.com/hanaml-lab/hana-agents/go/policy-engine/pkg/decide"
)

type decideBody struct {
	decide.Request
	Token string `json:"token,omitempty"`
}

type decideReply struct {
	decide.Decision
	ApprovalDigest string `json:"approval_digest,omitempty"`
}

func main() {
	dir := flag.String("policies", "policies/", "directory of .cedar files")
	addr := flag.String("addr", ":8181", "listen address")
	auditPath := flag.String("audit", "", "append decisions to this JSONL file")
	keyEnv := flag.String("token-key-env", "POLICY_TOKEN_KEY", "env var holding the capability token key")
	flag.Parse()

	engine, err := decide.Load(*dir)
	if err != nil {
		log.Fatal(err)
	}
	var logw *audit.Log
	if *auditPath != "" {
		f, err := os.OpenFile(*auditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		logw = audit.New(f)
	}
	key := []byte(os.Getenv(*keyEnv))
	approvals := approval.NewStore()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/decide", func(w http.ResponseWriter, r *http.Request) {
		var body decideBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req := body.Request
		if body.Token != "" {
			tok, err := capability.Verify(body.Token, key)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			req.Token = tok
		}
		d := engine.Decide(r.Context(), req)
		reply := decideReply{Decision: d}
		if d.Outcome == decide.Hold {
			digest, _ := approval.Digest(req.Principal, req.Action, req.Args)
			if _, err := approvals.Consume(digest, time.Now()); err == nil {
				reply.Decision = decide.Decision{Outcome: decide.Allow, Rule: d.Rule, Version: d.Version, Reason: "approved"}
			} else {
				reply.ApprovalDigest = digest
			}
		}
		if logw != nil {
			_ = logw.Write(audit.Entry{At: time.Now().UTC(), Principal: req.Principal, Action: req.Action,
				Args: req.Args, Outcome: reply.Outcome.String(), Rule: reply.Rule, PolicyVersion: d.Version})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})
	mux.HandleFunc("POST /v1/approvals", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Principal  string         `json:"principal"`
			Action     string         `json:"action"`
			Args       map[string]any `json:"args"`
			Approver   string         `json:"approver"`
			TTLSeconds int            `json:"ttl_seconds"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Approver == "" {
			http.Error(w, "principal, action, args and approver are required", http.StatusBadRequest)
			return
		}
		digest, err := approval.Digest(body.Principal, body.Action, body.Args)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ttl := time.Duration(body.TTLSeconds) * time.Second
		if ttl == 0 {
			ttl = 15 * time.Minute
		}
		approvals.Approve(approval.Approval{Digest: digest, Approver: body.Approver, Expires: time.Now().Add(ttl)})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"digest": digest})
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(engine.Version()))
	})
	log.Printf("policyd: policy version %s, listening on %s", engine.Version(), *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
