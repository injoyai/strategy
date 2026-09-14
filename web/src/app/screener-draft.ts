import type { components } from "../api/schema";

/**
 * The screener editor's local draft model and its conversion to the frozen
 * contract payload.
 *
 * The draft is deliberately separate from the wire types: the contract spells
 * input bindings and condition nodes as oneOf unions, so a form needs a
 * uniformly editable shape and one place that decides which union branch a
 * node becomes. Keeping that conversion pure means the editor's rules
 * (required fields, acceptable operators, which branch a node maps to) are
 * testable without rendering a form.
 */

export type ValueKind = "decimal" | "number" | "string" | "boolean" | "timestamp";
export type CompareOperator = "eq" | "ne" | "gt" | "gte" | "lt" | "lte";
export type Direction = "asc" | "desc";
export type ScoreDirection = "larger_is_better" | "smaller_is_better";

export type ValueDraft = { kind: ValueKind; text: string };

/** Editor-local parameter kinds: integers are a distinct declared type in the
 * factor registry, so the editor must not collapse them into numbers. */
export type ParamKind = ValueKind | "integer";

export type ParamDraft = { name: string; type: ParamKind; text: string };

export type BindingDraft = {
  key: string;
  bindingId: string;
  kind: "field" | "factor";
  dataset: string;
  field: string;
  factorId: string;
  factorVersion: string;
  params: ParamDraft[];
};

export type ConditionKind = "all" | "any" | "not" | "compare" | "range" | "set" | "missing";

export type NodeDraft = {
  key: string;
  nodeId: string;
  kind: ConditionKind;
  children: NodeDraft[];
  child: NodeDraft | null;
  bindingId: string;
  operator: CompareOperator;
  value: ValueDraft;
  lower: ValueDraft;
  upper: ValueDraft;
  lowerInclusive: boolean;
  upperInclusive: boolean;
  setValues: ValueDraft[];
  setNotIn: boolean;
  isMissing: boolean;
  /** Only `in` (not `not_in`) needs a non-empty set; the draft tracks it so the
   * editor can keep the last member while the mode toggles. */
  hasLower: boolean;
  hasUpper: boolean;
};

export type ScreenerDraft = {
  name: string;
  description: string;
  parentId: string;
  bindings: BindingDraft[];
  root: NodeDraft;
  rankingMode: "sort" | "score";
  sortFields: { bindingId: string; direction: Direction }[];
  scoreComponents: { bindingId: string; weight: string; direction: ScoreDirection }[];
  selectionMode: "all" | "top_n";
  topN: string;
  displayColumns: string[];
};

let keyCounter = 0;

/** Keys are UI identities only; the contract's node_id is edited explicitly. */
export function draftKey(prefix: string): string {
  keyCounter += 1;
  return `${prefix}-${keyCounter}`;
}

export function emptyValue(kind: ValueKind = "decimal"): ValueDraft {
  return { kind, text: "" };
}

/**
 * The contract spells a set condition as two branches (in / not_in) and a
 * missing condition as two (is_missing / is_present). These readers keep that
 * branch choice in one place instead of spreading `in` narrowing over the UI.
 */
export function setConditionMembers(node: components["schemas"]["ScreenCondition"]): { members: components["schemas"]["Value"][]; negated: boolean } {
  if ("not_in" in node) {
    return { members: node.not_in, negated: true };
  }
  if ("in" in node) {
    return { members: node.in, negated: false };
  }
  return { members: [], negated: false };
}

export function missingConditionIsMissing(node: components["schemas"]["ScreenCondition"]): boolean {
  return "is_missing" in node;
}

export function newNode(kind: ConditionKind, parentPath: string, index: number): NodeDraft {
  const key = draftKey("node");

  return {
    key,
    nodeId: `${parentPath}-${index + 1}`,
    kind,
    children: [],
    child: null,
    bindingId: "",
    operator: "gt",
    value: emptyValue(),
    lower: emptyValue(),
    upper: emptyValue(),
    lowerInclusive: true,
    upperInclusive: false,
    setValues: [emptyValue()],
    setNotIn: false,
    isMissing: true,
    hasLower: true,
    hasUpper: true,
  };
}

export function emptyScreenerDraft(): ScreenerDraft {
  return {
    name: "",
    description: "",
    parentId: "",
    bindings: [newBinding("px")],
    root: newNode("compare", "root", 0),
    rankingMode: "sort",
    sortFields: [{ bindingId: "px", direction: "desc" }],
    scoreComponents: [{ bindingId: "px", weight: "1", direction: "larger_is_better" }],
    selectionMode: "all",
    topN: "20",
    displayColumns: [],
  };
}

export function newBinding(bindingId: string): BindingDraft {
  return {
    key: draftKey("binding"),
    bindingId,
    kind: "field",
    dataset: "",
    field: "",
    factorId: "",
    factorVersion: "",
    params: [],
  };
}

/** Rebuilds an editable draft from a saved revision, so "基于此版本新建" edits a
 * copy instead of pretending the immutable revision can be modified. */
export function draftFromScreener(screener: components["schemas"]["Screener"]): ScreenerDraft {
  return {
    name: screener.name,
    description: screener.description ?? "",
    parentId: screener.id,
    bindings: screener.input_bindings.map((binding) =>
      binding.kind === "field"
        ? { ...newBinding(binding.binding_id), kind: "field" as const, dataset: binding.dataset, field: binding.field }
        : {
            ...newBinding(binding.binding_id),
            kind: "factor" as const,
            factorId: binding.factor_ref.id,
            factorVersion: binding.factor_ref.version,
            params: Object.entries(binding.params).map(([name, value]) => ({
              name,
              type: typeof value === "boolean" ? ("boolean" as const) : typeof value === "number" ? ("number" as const) : ("string" as const),
              text: String(value),
            })),
          },
    ),
    root: nodeDraftFromWire(screener.condition_tree),
    rankingMode: screener.ranking.mode,
    sortFields:
      screener.ranking.mode === "sort"
        ? screener.ranking.fields.map((field) => ({ bindingId: field.input.binding_id, direction: field.direction }))
        : [{ bindingId: "px", direction: "desc" }],
    scoreComponents:
      screener.ranking.mode === "score"
        ? screener.ranking.components.map((component) => ({
            bindingId: component.input.binding_id,
            weight: component.weight,
            direction: component.direction,
          }))
        : [{ bindingId: "px", weight: "1", direction: "larger_is_better" }],
    selectionMode: screener.selection.mode,
    topN: screener.selection.mode === "top_n" ? String(screener.selection.n) : "20",
    displayColumns: [...(screener.display_columns ?? [])],
  };
}

function nodeDraftFromWire(node: components["schemas"]["ScreenCondition"], index = 0): NodeDraft {
  const draft = newNode(node.kind as ConditionKind, "root", index);
  draft.nodeId = node.node_id;
  switch (node.kind) {
    case "all":
    case "any":
      draft.children = node.children.map((child, position) => nodeDraftFromWire(child, position));
      break;
    case "not":
      draft.child = nodeDraftFromWire(node.child, 0);
      break;
    case "compare":
      draft.bindingId = node.input.binding_id;
      draft.operator = node.operator as CompareOperator;
      draft.value = valueDraftFromWire(node.value);
      break;
    case "range":
      draft.bindingId = node.input.binding_id;
      draft.hasLower = node.lower !== null;
      draft.hasUpper = node.upper !== null;
      draft.lower = valueDraftFromWire(node.lower);
      draft.upper = valueDraftFromWire(node.upper);
      draft.lowerInclusive = node.lower_inclusive;
      draft.upperInclusive = node.upper_inclusive;
      break;
    case "set": {
      const set = setConditionMembers(node);
      draft.bindingId = node.input.binding_id;
      draft.setNotIn = set.negated;
      draft.setValues = set.members.map(valueDraftFromWire);
      break;
    }
    case "missing":
      draft.bindingId = node.input.binding_id;
      draft.isMissing = missingConditionIsMissing(node);
      break;
  }
  return draft;
}

function valueDraftFromWire(value: components["schemas"]["Value"] | null): ValueDraft {
  if (!value) {
    return emptyValue();
  }
  return { kind: value.kind as ValueKind, text: value.value === null || value.value === undefined ? "" : String(value.value) };
}

export type BuildResult =
  | { ok: true; value: components["schemas"]["ScreenerCreate"] }
  | { ok: false; message: string; focusId: string };

/**
 * Converts the draft into the frozen payload the API accepts. Every rejection
 * names the field to focus, because the same message is what a keyboard user
 * hears first.
 */
export function buildScreenerCreate(draft: ScreenerDraft): BuildResult {
  if (!draft.name.trim()) {
    return fail("请填写方案名称。", "screener-name");
  }
  if (draft.bindings.length === 0) {
    return fail("至少需要一个输入绑定。", "binding-add");
  }
  const bindingIds = new Set<string>();
  const bindings: components["schemas"]["ScreenInputBinding"][] = [];
  for (const [index, binding] of draft.bindings.entries()) {
    const focusId = `binding-id-${binding.key}`;
    const bindingId = binding.bindingId.trim();
    if (!bindingId) {
      return fail(`第 ${index + 1} 个输入缺少 binding_id。`, focusId);
    }
    if (bindingIds.has(bindingId)) {
      return fail(`binding_id 重复：${bindingId}。`, focusId);
    }
    bindingIds.add(bindingId);
    if (binding.kind === "field") {
      if (!binding.dataset.trim() || !binding.field.trim()) {
        return fail(`绑定 ${bindingId} 需要数据集与字段。`, `binding-dataset-${binding.key}`);
      }
      bindings.push({ binding_id: bindingId, kind: "field", dataset: binding.dataset.trim(), field: binding.field.trim() });
      continue;
    }
    if (!binding.factorId.trim() || !binding.factorVersion.trim()) {
      return fail(`绑定 ${bindingId} 需要因子 id 与 version。`, `binding-factor-${binding.key}`);
    }
    const params: Record<string, unknown> = {};
    for (const param of binding.params) {
      const name = param.name.trim();
      if (!name) {
        return fail(`绑定 ${bindingId} 的参数缺少名称。`, `param-name-${binding.key}`);
      }
      const parsed = parseParamValue(param, `param-value-${binding.key}`);
      if (!parsed.ok) {
        return parsed;
      }
      params[name] = parsed.value;
    }
    bindings.push({
      binding_id: bindingId,
      kind: "factor",
      factor_ref: { id: binding.factorId.trim(), version: binding.factorVersion.trim() },
      params,
    });
  }

  const condition = buildNode(draft.root, bindingIds);
  if (!condition.ok) {
    return condition;
  }

  let ranking: components["schemas"]["ScreenRanking"];
  if (draft.rankingMode === "sort") {
    if (draft.sortFields.length === 0) {
      return fail("排序模式至少需要一个排序字段。", "ranking-add-sort");
    }
    const fields: components["schemas"]["ScreenRankComponent"][] = [];
    for (const field of draft.sortFields) {
      if (!bindingIds.has(field.bindingId)) {
        return fail("排序字段必须引用已声明的输入。", "ranking-sort-0");
      }
      fields.push({ input: { binding_id: field.bindingId }, direction: field.direction });
    }
    ranking = { mode: "sort", fields };
  } else {
    if (draft.scoreComponents.length === 0) {
      return fail("评分模式至少需要一个分量。", "ranking-add-score");
    }
    const components: components["schemas"]["ScreenScoreComponent"][] = [];
    let weightSum = 0;
    for (const [index, component] of draft.scoreComponents.entries()) {
      if (!bindingIds.has(component.bindingId)) {
        return fail("评分分量必须引用已声明的输入。", `ranking-score-binding-${index}`);
      }
      const weight = Number(component.weight);
      if (!Number.isFinite(weight) || weight < 0) {
        return fail("权重必须是不小于 0 的数字。", `ranking-score-weight-${index}`);
      }
      weightSum += weight;
      components.push({
        input: { binding_id: component.bindingId },
        weight: component.weight.trim(),
        direction: component.direction,
      });
    }
    // Weights are submitted normalized; the server never re-distributes them,
    // so a sum that is not 1 would silently change every score.
    if (Math.abs(weightSum - 1) > 1e-9) {
      return fail(`评分权重之和必须为 1（当前 ${weightSum}）。`, "ranking-score-weight-0");
    }
    ranking = { mode: "score", components };
  }

  let selection: components["schemas"]["ScreenSelection"];
  if (draft.selectionMode === "all") {
    selection = { mode: "all" };
  } else {
    const n = Number.parseInt(draft.topN.trim(), 10);
    if (!Number.isInteger(n) || n < 1) {
      return fail("top_n 必须是正整数。", "selection-top-n");
    }
    selection = { mode: "top_n", n };
  }

  for (const column of draft.displayColumns) {
    if (!bindingIds.has(column)) {
      return fail("展示列必须引用已声明的输入。", "display-columns");
    }
  }

  return {
    ok: true,
    value: {
      name: draft.name.trim(),
      ...(draft.description.trim() ? { description: draft.description.trim() } : {}),
      ...(draft.parentId ? { parent_id: draft.parentId } : {}),
      input_bindings: bindings,
      condition_tree: condition.value,
      ranking,
      selection,
      display_columns: draft.displayColumns,
    },
  };
}

function parseParamValue(param: ParamDraft, focusId: string): { ok: true; value: unknown } | { ok: false; message: string; focusId: string } {
  const text = param.text.trim();
  switch (param.type) {
    case "boolean":
      if (text === "true" || text === "false") {
        return { ok: true, value: text === "true" };
      }
      return { ok: false, message: "布尔参数只能填 true 或 false。", focusId };
    case "integer": {
      const parsed = Number(text);
      return Number.isInteger(parsed) ? { ok: true, value: parsed } : { ok: false, message: "整数参数必须是整数。", focusId };
    }
    case "number":
    case "decimal": {
      // Numbers cross the wire as JSON numbers; decimals stay strings so no
      // precision is lost in JavaScript.
      if (param.type === "decimal") {
        return text ? { ok: true, value: text } : { ok: false, message: "参数值不能为空。", focusId };
      }
      const parsed = Number(text);
      return Number.isFinite(parsed) ? { ok: true, value: parsed } : { ok: false, message: "参数必须是数字。", focusId };
    }
    default:
      return text ? { ok: true, value: text } : { ok: false, message: "参数值不能为空。", focusId };
  }
}

type NodeResult =
  | { ok: true; value: components["schemas"]["ScreenCondition"] }
  | { ok: false; message: string; focusId: string };

function buildNode(node: NodeDraft, bindingIds: Set<string>): NodeResult {
  const focusId = `node-${node.key}`;
  if (!node.nodeId.trim()) {
    return { ok: false, message: "每个条件节点都需要 node_id。", focusId };
  }
  switch (node.kind) {
    case "all":
    case "any": {
      if (node.children.length === 0) {
        return { ok: false, message: `${node.nodeId} 至少需要一个子节点。`, focusId };
      }
      const children: components["schemas"]["ScreenCondition"][] = [];
      for (const child of node.children) {
        const built = buildNode(child, bindingIds);
        if (!built.ok) {
          return built;
        }
        children.push(built.value);
      }
      return { ok: true, value: { node_id: node.nodeId.trim(), kind: node.kind, children } };
    }
    case "not": {
      if (!node.child) {
        return { ok: false, message: `${node.nodeId} 的 NOT 需要一个子节点。`, focusId };
      }
      const child = buildNode(node.child, bindingIds);
      if (!child.ok) {
        return child;
      }
      return { ok: true, value: { node_id: node.nodeId.trim(), kind: "not", child: child.value } };
    }
    default:
      break;
  }
  if (!bindingIds.has(node.bindingId)) {
    return { ok: false, message: `节点 ${node.nodeId} 必须引用已声明的输入。`, focusId };
  }
  const input = { binding_id: node.bindingId };
  switch (node.kind) {
    case "compare": {
      const value = buildValue(node.value, focusId);
      if (!value.ok) {
        return value;
      }
      return { ok: true, value: { node_id: node.nodeId.trim(), kind: "compare", input, operator: node.operator, value: value.value } };
    }
    case "range": {
      if (!node.hasLower && !node.hasUpper) {
        return { ok: false, message: `区间节点 ${node.nodeId} 至少需要一个边界。`, focusId };
      }
      let lower: components["schemas"]["Value"] | null = null;
      let upper: components["schemas"]["Value"] | null = null;
      if (node.hasLower) {
        const built = buildValue(node.lower, focusId);
        if (!built.ok) {
          return built;
        }
        lower = built.value;
      }
      if (node.hasUpper) {
        const built = buildValue(node.upper, focusId);
        if (!built.ok) {
          return built;
        }
        upper = built.value;
      }
      return {
        ok: true,
        value: {
          node_id: node.nodeId.trim(),
          kind: "range",
          input,
          lower,
          upper,
          lower_inclusive: node.lowerInclusive,
          upper_inclusive: node.upperInclusive,
        },
      };
    }
    case "set": {
      const values: components["schemas"]["Value"][] = [];
      for (const member of node.setValues) {
        const built = buildValue(member, focusId);
        if (!built.ok) {
          return built;
        }
        values.push(built.value);
      }
      if (values.length === 0) {
        return { ok: false, message: `集合节点 ${node.nodeId} 至少需要一个值。`, focusId };
      }
      return {
        ok: true,
        value: node.setNotIn
          ? { node_id: node.nodeId.trim(), kind: "set", input, not_in: values }
          : { node_id: node.nodeId.trim(), kind: "set", input, in: values },
      };
    }
    case "missing":
      return {
        ok: true,
        value: node.isMissing
          ? { node_id: node.nodeId.trim(), kind: "missing", input, is_missing: true }
          : { node_id: node.nodeId.trim(), kind: "missing", input, is_present: true },
      };
    default:
      return { ok: false, message: `未知的节点类型：${node.kind}`, focusId };
  }
}

function buildValue(
  draft: ValueDraft,
  focusId: string,
): { ok: true; value: components["schemas"]["Value"] } | { ok: false; message: string; focusId: string } {
  const text = draft.text.trim();
  if (!text) {
    return { ok: false, message: "条件值不能为空。", focusId };
  }
  // A present value always carries an explicit null missing_reason: absence of a
  // reason is what distinguishes "no value" from "a value that is null".
  switch (draft.kind) {
    case "boolean":
      if (text !== "true" && text !== "false") {
        return { ok: false, message: "布尔值只能填 true 或 false。", focusId };
      }
      return { ok: true, value: { kind: "boolean", value: text === "true", missing_reason: null } };
    case "number": {
      const parsed = Number(text);
      if (!Number.isFinite(parsed)) {
        return { ok: false, message: "数值条件的值必须是数字。", focusId };
      }
      return { ok: true, value: { kind: "number", value: parsed, missing_reason: null } };
    }
    default:
      // decimal and timestamp stay strings: the server owns parsing and rejects
      // anything it cannot read, so the browser never rounds a decimal.
      return { ok: true, value: { kind: draft.kind, value: text, missing_reason: null } };
  }
}

function fail(message: string, focusId: string): BuildResult {
  return { ok: false, message, focusId };
}

/** The condition tree editor's child path, kept stable across renames. */
export function childPath(node: NodeDraft): string {
  return node.nodeId || "node";
}
