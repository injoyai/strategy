import { describe, expect, it } from "vitest";
import {
  buildScreenerCreate,
  draftFromScreener,
  emptyScreenerDraft,
  newNode,
  type ConditionKind,
  type ScreenerDraft,
} from "./screener-draft";

/**
 * The editor's conversion rules are the part of the screening UI that can
 * silently produce a wrong frozen rule set, so they are tested without
 * rendering a form: which union branch a node becomes, what is refused, and
 * what an existing revision round-trips to.
 */

function draftWithNode(kind: ConditionKind): ScreenerDraft {
  const draft = emptyScreenerDraft();
  // The default binding must resolve, otherwise every case would fail on the
  // binding itself instead of the rule under test.
  draft.bindings = [{ ...draft.bindings[0]!, dataset: "bar", field: "close" }];
  const node = newNode(kind, "root", 0);
  node.nodeId = "root";
  node.bindingId = "px";
  draft.root = node;
  return draft;
}

describe("buildScreenerCreate", () => {
  it("freezes a compare condition, sort ranking and top_n selection", () => {
    const draft = draftWithNode("compare");
    draft.name = "质量动量池";
    draft.root.value = { kind: "decimal", text: "10.5" };
    draft.root.operator = "gt";
    draft.selectionMode = "top_n";
    draft.topN = "20";
    draft.displayColumns = ["px"];

    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(true);
    if (!built.ok) return;
    expect(built.value.condition_tree).toEqual({
      node_id: "root",
      kind: "compare",
      input: { binding_id: "px" },
      operator: "gt",
      value: { kind: "decimal", value: "10.5", missing_reason: null },
    });
    expect(built.value.ranking).toEqual({ mode: "sort", fields: [{ input: { binding_id: "px" }, direction: "desc" }] });
    expect(built.value.selection).toEqual({ mode: "top_n", n: 20 });
    expect(built.value.display_columns).toEqual(["px"]);
  });

  it("keeps a decimal as a string so no precision is lost in JavaScript", () => {
    const draft = draftWithNode("compare");
    draft.name = "x";
    draft.root.value = { kind: "decimal", text: "10.40" };
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(true);
    if (!built.ok) return;
    const node = built.value.condition_tree;
    expect(node.kind).toBe("compare");
    if (node.kind !== "compare") return;
    expect(node.value?.value).toBe("10.40");
  });

  it("maps a set node onto the not_in branch when negated", () => {
    const draft = draftWithNode("set");
    draft.name = "x";
    draft.root.setNotIn = true;
    draft.root.setValues = [{ kind: "string", text: "ST" }, { kind: "string", text: "PT" }];
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(true);
    if (!built.ok) return;
    expect(built.value.condition_tree).toEqual({
      node_id: "root",
      kind: "set",
      input: { binding_id: "px" },
      not_in: [
        { kind: "string", value: "ST", missing_reason: null },
        { kind: "string", value: "PT", missing_reason: null },
      ],
    });
  });

  it("refuses a range node with no bound instead of sending an open-ended filter", () => {
    const draft = draftWithNode("range");
    draft.name = "x";
    draft.root.hasLower = false;
    draft.root.hasUpper = false;
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(false);
    if (built.ok) return;
    expect(built.focusId).toBe(`node-${draft.root.key}`);
  });

  it("refuses weights that do not sum to one", () => {
    const draft = draftWithNode("compare");
    draft.name = "x";
    draft.root.value = { kind: "decimal", text: "1" };
    draft.rankingMode = "score";
    draft.scoreComponents = [
      { bindingId: "px", weight: "0.6", direction: "larger_is_better" },
      { bindingId: "px", weight: "0.6", direction: "larger_is_better" },
    ];
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(false);
    if (built.ok) return;
    expect(built.message).toContain("权重之和必须为 1");
  });

  it("refuses duplicate binding ids", () => {
    const draft = draftWithNode("compare");
    draft.name = "x";
    draft.root.value = { kind: "decimal", text: "1" };
    draft.bindings = [
      { ...draft.bindings[0]!, bindingId: "px", kind: "field", dataset: "bar", field: "close" },
      { ...draft.bindings[0]!, bindingId: "px", kind: "field", dataset: "bar", field: "pe" },
    ];
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(false);
    if (built.ok) return;
    expect(built.message).toContain("binding_id 重复");
  });

  it("refuses a condition that references an undeclared binding", () => {
    const draft = draftWithNode("missing");
    draft.name = "x";
    draft.root.bindingId = "absent";
    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(false);
    if (built.ok) return;
    expect(built.message).toContain("必须引用已声明的输入");
  });

  it("round-trips an existing revision into an editable draft that rebuilds identically", () => {
    const screener = {
      id: "scr_1",
      version: "v2",
      name: "质量动量池",
      description: "示例",
      input_bindings: [
        { binding_id: "px", kind: "field" as const, dataset: "bar", field: "close" },
        { binding_id: "mom", kind: "factor" as const, factor_ref: { id: "momentum", version: "1.0.0" }, params: { n: 20 } },
      ],
      condition_tree: {
        node_id: "root",
        kind: "all" as const,
        children: [
          { node_id: "gt", kind: "compare" as const, input: { binding_id: "px" }, operator: "gt" as const, value: { kind: "decimal" as const, value: "10", missing_reason: null } },
          { node_id: "miss", kind: "missing" as const, input: { binding_id: "mom" }, is_present: true },
        ],
      },
      ranking: {
        mode: "score" as const,
        components: [{ input: { binding_id: "mom" }, weight: "1", direction: "larger_is_better" as const }],
      },
      selection: { mode: "top_n" as const, n: 5 },
      display_columns: ["px", "mom"],
      rule_schema_version: "screener-rule/1",
      created_at: "2026-01-15T12:00:00Z",
    };

    const draft = draftFromScreener(screener);
    expect(draft.parentId).toBe("scr_1");
    expect(draft.bindings.map((binding) => binding.bindingId)).toEqual(["px", "mom"]);
    expect(draft.root.children).toHaveLength(2);

    const built = buildScreenerCreate(draft);
    expect(built.ok).toBe(true);
    if (!built.ok) return;
    // The rebuilt payload keeps the parent identity, both binding branches and
    // the is_present branch the revision was saved with.
    expect(built.value.parent_id).toBe("scr_1");
    expect(built.value.input_bindings).toEqual(screener.input_bindings);
    expect(built.value.condition_tree).toEqual(screener.condition_tree);
    expect(built.value.ranking).toEqual(screener.ranking);
    expect(built.value.selection).toEqual(screener.selection);
  });
});
