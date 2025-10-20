/** Typed client for policyd (`go/policy-engine/cmd/policyd`). */

export type Outcome = "allow" | "hold" | "forbid";

export interface DecideRequest {
  principal: string;
  action: string;
  args: Record<string, unknown>;
  context?: Record<string, unknown>;
  /** A signed capability token; anything outside it is forbidden before policy runs. */
  token?: string;
}

export interface Decision {
  outcome: Outcome;
  /** The rule that decided: an @id, an @hold name, a file:line, "capability" or "default-deny". */
  rule: string;
  policyVersion: string;
  reason?: string;
  /** Present on a hold: approve this digest to let the identical call through once. */
  approvalDigest?: string;
}

export class PolicyError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export class ForbiddenAction extends Error {
  readonly decision: Decision;
  constructor(decision: Decision) {
    super(`forbidden by ${decision.rule}${decision.reason ? `: ${decision.reason}` : ""}`);
    this.decision = decision;
  }
}

export class HeldForApproval extends Error {
  readonly decision: Decision;
  constructor(decision: Decision) {
    super(`held for approval by ${decision.rule}`);
    this.decision = decision;
  }
}

type Fetch = typeof fetch;

export class PolicyClient {
  private readonly baseUrl: string;
  private readonly fetchImpl: Fetch;

  constructor(baseUrl: string, fetchImpl: Fetch = fetch) {
    this.baseUrl = baseUrl;
    this.fetchImpl = fetchImpl;
  }

  private async post<T>(path: string, body: unknown): Promise<T> {
    const res = await this.fetchImpl(new URL(path, this.baseUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new PolicyError(await res.text(), res.status);
    return (await res.json()) as T;
  }

  async decide(req: DecideRequest): Promise<Decision> {
    const raw = await this.post<{
      outcome: Outcome;
      rule: string;
      policy_version: string;
      reason?: string;
      approval_digest?: string;
    }>("/v1/decide", req);
    return {
      outcome: raw.outcome,
      rule: raw.rule,
      policyVersion: raw.policy_version,
      reason: raw.reason,
      approvalDigest: raw.approval_digest,
    };
  }

  async approve(req: Omit<DecideRequest, "context" | "token">, approver: string, ttlSeconds = 900): Promise<string> {
    const { digest } = await this.post<{ digest: string }>("/v1/approvals", {
      ...req,
      approver,
      ttl_seconds: ttlSeconds,
    });
    return digest;
  }

  /**
   * Wrap a tool so every call is decided first. Allowed calls run; held calls throw
   * HeldForApproval with the digest to approve; forbidden calls throw ForbiddenAction.
   */
  guard<A extends Record<string, unknown>, R>(
    principal: string,
    action: string,
    tool: (args: A) => Promise<R>,
    context: () => Record<string, unknown> = () => ({}),
  ): (args: A) => Promise<R> {
    return async (args: A) => {
      const d = await this.decide({ principal, action, args, context: context() });
      if (d.outcome === "forbid") throw new ForbiddenAction(d);
      if (d.outcome === "hold") throw new HeldForApproval(d);
      return tool(args);
    };
  }
}
