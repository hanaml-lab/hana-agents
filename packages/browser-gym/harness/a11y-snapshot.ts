/**
 * Prune a Chromium accessibility tree (CDP `Accessibility.getFullAXTree` plus the bounding
 * boxes from `DOM.getBoxModel`) to the same line format as browser_gym/observe.py:
 * off-screen and ignored nodes are dropped first, then the focused node is put first, then the
 * list is truncated to a token budget.
 */

export interface AXNode {
  nodeId: string;
  ignored?: boolean;
  role?: { value: string };
  name?: { value: string };
  value?: { value: string | number };
  properties?: { name: string; value: { value: unknown } }[];
  /** top of the node's box in page pixels, filled in from DOM.getBoxModel */
  top?: number;
}

export interface Snapshot {
  lines: string[];
  hidden: number;
  truncated: number;
}

const SKIP_ROLES = new Set(["none", "generic", "InlineTextBox", "LineBreak", "StaticText", "RootWebArea"]);

const tokens = (line: string) => Math.max(1, Math.floor(line.length / 4));

function focused(n: AXNode): boolean {
  return (n.properties ?? []).some((p) => p.name === "focused" && p.value.value === true);
}

export function line(n: AXNode): string {
  const value = n.value?.value !== undefined && n.value.value !== "" ? ` value="${n.value.value}"` : "";
  return `[${n.nodeId}] ${n.role?.value ?? "node"} "${n.name?.value ?? ""}"${value}`;
}

export function snapshot(nodes: AXNode[], scrollY: number, viewport: number, budget = 2048): Snapshot {
  const meaningful = nodes.filter((n) => !n.ignored && n.role && !SKIP_ROLES.has(n.role.value));
  const visible = meaningful.filter((n) => n.top !== undefined && n.top >= scrollY && n.top < scrollY + viewport);
  visible.sort((a, b) => Number(focused(b)) - Number(focused(a)) || (a.top ?? 0) - (b.top ?? 0));
  const lines: string[] = [];
  let used = 0;
  for (const n of visible) {
    const l = line(n);
    if (used + tokens(l) > budget) break;
    lines.push(l);
    used += tokens(l);
  }
  return { lines, hidden: meaningful.length - visible.length, truncated: visible.length - lines.length };
}
