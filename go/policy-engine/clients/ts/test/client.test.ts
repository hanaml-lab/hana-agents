import { strict as assert } from "node:assert";
import { test } from "node:test";

import { ForbiddenAction, HeldForApproval, PolicyClient } from "../src/index.ts";

function fakePolicyd(): typeof fetch {
  const approved = new Set<string>();
  return (async (url: URL, init: RequestInit) => {
    const body = JSON.parse(String(init.body));
    const digest = `${body.action}:${JSON.stringify(body.args)}`;
    if (url.pathname === "/v1/approvals") {
      approved.add(digest);
      return new Response(JSON.stringify({ digest }));
    }
    const amount = Number(body.args.amount ?? 0);
    if (body.action !== "payments.send") {
      return new Response(JSON.stringify({ outcome: "forbid", rule: "default-deny", policy_version: "v1" }));
    }
    if (amount > 1000 && !approved.delete(digest)) {
      return new Response(
        JSON.stringify({ outcome: "hold", rule: "over-limit", policy_version: "v1", approval_digest: digest }),
      );
    }
    return new Response(JSON.stringify({ outcome: "allow", rule: "within-limit", policy_version: "v1" }));
  }) as typeof fetch;
}

test("decision carries the rule and the digest to approve", async () => {
  const client = new PolicyClient("http://policyd", fakePolicyd());
  const d = await client.decide({ principal: "agent:ap-clerk", action: "payments.send", args: { amount: 4200 } });
  assert.equal(d.outcome, "hold");
  assert.equal(d.rule, "over-limit");
  assert.ok(d.approvalDigest);
});

test("guard runs allowed calls, throws on hold and forbid, and honours one approval", async () => {
  const client = new PolicyClient("http://policyd", fakePolicyd());
  let sent = 0;
  const send = client.guard("agent:ap-clerk", "payments.send", async (_: { amount: number }) => ++sent);
  assert.equal(await send({ amount: 10 }), 1);
  await assert.rejects(send({ amount: 4200 }), HeldForApproval);
  await client.approve({ principal: "agent:ap-clerk", action: "payments.send", args: { amount: 4200 } }, "maria");
  assert.equal(await send({ amount: 4200 }), 2);
  await assert.rejects(send({ amount: 4200 }), HeldForApproval);
  const refund = client.guard("agent:ap-clerk", "payments.refund", async () => 0);
  await assert.rejects(refund({}), ForbiddenAction);
});
