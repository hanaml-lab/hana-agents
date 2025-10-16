------------------------------- MODULE Decide -------------------------------
(***************************************************************************)
(* The decision table of policy-engine. A request is matched by any subset *)
(* of permit, hold (a permit annotated @hold) and forbid rules, and may or  *)
(* may not be inside its capability token. The outcome must be the         *)
(* strictest of the matched rules, default-deny when no permit matches,    *)
(* and forbid whenever the token does not cover the request.               *)
(*                                                                         *)
(* TLC enumerates every state; each one is exported as a case in           *)
(* pkg/decide/testdata/tla-traces.json and replayed by decide_test.go.     *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets

Outcomes == {"allow", "hold", "forbid"}
Rank(o) == CASE o = "allow" -> 0 [] o = "hold" -> 1 [] o = "forbid" -> 2

VARIABLES inScope, matched, outcome

Kinds == {"permit", "hold", "forbid"}

Decide(scope, m) ==
    IF ~scope THEN "forbid"
    ELSE IF "forbid" \in m THEN "forbid"
    ELSE IF "hold" \in m THEN "hold"
    ELSE IF "permit" \in m THEN "allow"
    ELSE "forbid"

Init ==
    /\ inScope \in BOOLEAN
    /\ matched \in SUBSET Kinds
    /\ outcome = Decide(inScope, matched)

Next == UNCHANGED <<inScope, matched, outcome>>

Spec == Init /\ [][Next]_<<inScope, matched, outcome>>

\* Safety: nothing outside the token runs; a matched forbid is never overridden;
\* a matched hold is never silently allowed.
NoEscalation ==
    /\ ~inScope => outcome = "forbid"
    /\ "forbid" \in matched => outcome = "forbid"
    /\ ("hold" \in matched /\ inScope /\ "forbid" \notin matched) => outcome = "hold"
    /\ outcome = "allow" => ("permit" \in matched /\ inScope)

THEOREM Spec => []NoEscalation
=============================================================================
