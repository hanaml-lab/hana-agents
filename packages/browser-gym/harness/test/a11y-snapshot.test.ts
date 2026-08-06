import { strict as assert } from "node:assert";
import { test } from "node:test";

import { type AXNode, snapshot } from "../a11y-snapshot.ts";

const node = (id: string, role: string, name: string, top: number, extra: Partial<AXNode> = {}): AXNode => ({
  nodeId: id,
  role: { value: role },
  name: { value: name },
  top,
  ...extra,
});

test("prunes off-screen and ignored nodes before truncating, focused first", () => {
  const nodes: AXNode[] = [
    ...Array.from({ length: 40 }, (_, i) => node(`n${i}`, "link", `item ${i}`, i * 100)),
    node("f", "textbox", "Search", 50, { properties: [{ name: "focused", value: { value: true } }] }),
    node("g", "generic", "", 10),
    node("x", "button", "Hidden", 20, { ignored: true }),
  ];
  const s = snapshot(nodes, 0, 500);
  assert.equal(s.lines[0], '[f] textbox "Search"');
  assert.equal(s.lines.length, 6);
  assert.equal(s.hidden, 35);
  assert.equal(s.truncated, 0);
});

test("respects the token budget", () => {
  const nodes = Array.from({ length: 10 }, (_, i) => node(`n${i}`, "link", "a fairly long link name here", i));
  const s = snapshot(nodes, 0, 1000, 20);
  assert.equal(s.lines.length, 2);
  assert.equal(s.truncated, 8);
});
