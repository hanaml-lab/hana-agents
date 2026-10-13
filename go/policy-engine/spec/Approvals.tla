------------------------------ MODULE Approvals ------------------------------
(***************************************************************************)
(* How agent-runtime spends approvals and calls tools, as its journal      *)
(* enforces it. A step's call is held by policy; a person approves the     *)
(* digest of its exact arguments. A worker journals the decision that     *)
(* spends the approval, calls the tool and journals the result; it may     *)
(* crash between any two of these and resume from the journal. The         *)
(* external system deduplicates on the key. The step can come back (a      *)
(* forbid or a refusal before the call, the critic after it): the planner  *)
(* revises it into a new attempt, with a new key and maybe new arguments,  *)
(* which must be approved again.                                           *)
(*                                                                         *)
(* Checked by TLC (Approvals.cfg):                                         *)
(*   NoStaleApproval  an effect runs only with an approval of exactly its  *)
(*                    arguments, granted before the decision;              *)
(*   SingleUse        one approval pays for at most one attempt;           *)
(*   ExactlyOnce      a key takes effect at most once, whatever the        *)
(*                    crashes, and a journalled result has its effect;     *)
(*   NoDoubleCharge   the cost charged equals the cost of distinct         *)
(*                    effects, so a retry never spends twice.              *)
(* runtime.Engine implements these with approval.Digest, the               *)
(* policy_decided event (approval_used) and tools.Key.                     *)
(***************************************************************************)
EXTENDS Naturals, FiniteSets, TLC

CONSTANTS Args,        \* the argument values a call can have
          MaxAttempt,  \* how often the planner may revise the step
          Cost         \* the cost of one effect

VARIABLES
    att,        \* the current attempt of the step (its idempotency key)
    args,       \* the arguments of the current attempt
    pc,         \* the worker: "idle", "permitted", "called", "recorded" or "done"
    granted,    \* approvals a person has given, as a set of <<n, args>> (n numbers them)
    spentOn,    \* approval number -> the attempt that spent it
    decided,    \* attempt -> the args it was permitted with (journalled policy_decided)
    effects,    \* effects in the external system, as <<key, args>>
    journalled, \* attempts whose result is in the journal
    charged     \* total cost charged

vars == <<att, args, pc, granted, spentOn, decided, effects, journalled, charged>>

Approvals == 1..MaxAttempt + 1

Init ==
    /\ att = 0
    /\ args \in Args
    /\ pc = "idle"
    /\ granted = {}
    /\ spentOn = [n \in {} |-> 0]
    /\ decided = [a \in {} |-> 0]
    /\ effects = {}
    /\ journalled = {}
    /\ charged = 0

Unspent == {g \in granted : g[1] \notin DOMAIN spentOn}

\* A person approves the held call: an approval bound to its exact arguments.
Approve ==
    /\ pc = "idle" /\ att \notin DOMAIN decided
    /\ \E n \in Approvals \ {g[1] : g \in granted} :
         granted' = granted \cup {<<n, args>>}
    /\ UNCHANGED <<att, args, pc, spentOn, decided, effects, journalled, charged>>

\* The worker journals a decision that spends one unspent approval of exactly these arguments.
Decide ==
    /\ pc = "idle" /\ att \notin DOMAIN decided /\ att \notin journalled
    /\ \E g \in Unspent :
         /\ g[2] = args
         /\ spentOn' = spentOn @@ (g[1] :> att)
    /\ decided' = decided @@ (att :> args)
    /\ pc' = "permitted"
    /\ UNCHANGED <<att, args, granted, effects, journalled, charged>>

\* A resumed worker finds the journalled decision, or the journalled result, and goes on
\* from there without asking again or calling again.
Resume ==
    /\ pc = "idle" /\ att \in DOMAIN decided
    /\ pc' = IF att \in journalled THEN "recorded" ELSE "permitted"
    /\ UNCHANGED <<att, args, granted, spentOn, decided, effects, journalled, charged>>

\* The tool call, with the attempt's key. The external system deduplicates on it.
Call ==
    /\ pc = "permitted"
    /\ IF \E e \in effects : e[1] = att
         THEN UNCHANGED <<effects, charged>>
         ELSE /\ effects' = effects \cup {<<att, decided[att]>>}
              /\ charged' = charged + Cost
    /\ pc' = "called"
    /\ UNCHANGED <<att, args, granted, spentOn, decided, journalled>>

Record ==
    /\ pc = "called"
    /\ journalled' = journalled \cup {att}
    /\ pc' = "recorded"
    /\ UNCHANGED <<att, args, granted, spentOn, decided, effects, charged>>

\* The critic accepts the result.
Accept ==
    /\ pc = "recorded"
    /\ pc' = "done"
    /\ UNCHANGED <<att, args, granted, spentOn, decided, effects, journalled, charged>>

\* The process dies; whatever is not journalled is lost from memory.
Crash ==
    /\ pc \in {"permitted", "called", "recorded"}
    /\ pc' = "idle"
    /\ UNCHANGED <<att, args, granted, spentOn, decided, effects, journalled, charged>>

\* The step comes back to the planner, before the call (forbidden or refused) or after it
\* (the critic rejects the result), and is revised into a new attempt.
Revise ==
    /\ \/ pc = "idle" /\ att \notin DOMAIN decided
       \/ pc = "recorded"
    /\ att < MaxAttempt
    /\ att' = att + 1
    /\ args' \in Args
    /\ pc' = "idle"
    /\ UNCHANGED <<granted, spentOn, decided, effects, journalled, charged>>

Next == Approve \/ Decide \/ Resume \/ Call \/ Record \/ Accept \/ Crash \/ Revise

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ att \in 0..MaxAttempt
    /\ pc \in {"idle", "permitted", "called", "recorded", "done"}
    /\ charged \in Nat

NoStaleApproval ==
    \A e \in effects : \E n \in DOMAIN spentOn :
        /\ spentOn[n] = e[1]
        /\ <<n, e[2]>> \in granted

SingleUse ==
    \A a \in DOMAIN decided : Cardinality({n \in DOMAIN spentOn : spentOn[n] = a}) = 1

ExactlyOnce ==
    /\ \A k \in 0..MaxAttempt : Cardinality({e \in effects : e[1] = k}) <= 1
    /\ \A k \in journalled : \E e \in effects : e[1] = k

NoDoubleCharge == charged = Cost * Cardinality(effects)
=============================================================================
