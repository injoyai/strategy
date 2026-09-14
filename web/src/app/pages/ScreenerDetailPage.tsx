import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import type { components } from "../../api/schema";
import { usePreflightScreenRun, useScreener, useSnapshots, useStartScreenRun, useUniverses } from "../../api/hooks";
import { missingConditionIsMissing, setConditionMembers } from "../screener-draft";
import { EmptyState, Field, IssueList, PageHeader, QueryError, QueryLoading, Section, formatDateTime } from "../ui";

/**
 * One immutable screener revision plus the run launcher. The revision is the
 * research fact; the run form is a local draft that only becomes facts when the
 * server freezes it into a ScreenRun.
 */
export function ScreenerDetailPage() {
  const params = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const id = params.id ?? "";
  const version = searchParams.get("version") ?? "";
  const ref = useMemo(() => (id && version ? { id, version } : undefined), [id, version]);
  const screener = useScreener(ref);

  if (!id) {
    return <EmptyState title="尚未选择方案" description="在方案目录中选择一个修订。" />;
  }
  if (!version) {
    return <EmptyState title="缺少版本参数" description="方案详情必须带有 version 查询参数；请从方案目录进入。" />;
  }
  if (screener.isPending) return <QueryLoading label="正在读取方案修订" />;
  if (screener.isError) return <QueryError error={screener.error} onRetry={() => void screener.refetch()} />;
  if (!screener.data) return null;
  const detail = screener.data;

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="选股 / SCREENER VERSION"
        title={detail.name}
        description={detail.description || "该修订是不可变的：修改会创建新的版本，旧版本与它跑过的结果保持不变。"}
        action={
          <div className="header-actions">
            <Link className="button-link secondary-link" to={`/screeners/new?from=${encodeURIComponent(detail.id)}&fromVersion=${encodeURIComponent(detail.version)}`}>基于此版本新建</Link>
            <Link className="button-link secondary-link" to="/screeners">返回方案目录</Link>
          </div>
        }
      />

      <Section title="冻结的规则" description="服务端保存的是规范化后的载荷：绑定、条件树、排名与数量，加上展示列。">
        <dl className="detail-grid">
          <div><dt>方案修订</dt><dd className="mono">{detail.id}@{detail.version}</dd></div>
          <div><dt>规则 schema</dt><dd className="mono">{detail.rule_schema_version}</dd></div>
          <div><dt>创建时间</dt><dd>{formatDateTime(detail.created_at)}</dd></div>
          <div><dt>上级修订</dt><dd className="mono">{detail.parent_id ? detail.parent_id : "—"}</dd></div>
        </dl>

        <div className="subsection-heading"><h3>输入绑定</h3></div>
        <div className="table-scroll">
          <table className="data-table compact-table">
            <caption className="sr-only">输入绑定</caption>
            <thead><tr><th scope="col">binding_id</th><th scope="col">类型</th><th scope="col">来源</th><th scope="col">参数</th></tr></thead>
            <tbody>
              {detail.input_bindings.map((binding) => (
                <tr key={binding.binding_id}>
                  <th scope="row" className="mono">{binding.binding_id}</th>
                  <td>{binding.kind === "field" ? "字段" : "因子"}</td>
                  <td className="mono">
                    {binding.kind === "field" ? `${binding.dataset}/${binding.field}` : `${binding.factor_ref.id}@${binding.factor_ref.version}`}
                  </td>
                  <td className="mono">
                    {binding.kind === "factor" && Object.keys(binding.params).length > 0
                      ? Object.entries(binding.params).map(([name, value]) => `${name}=${String(value)}`).join(", ")
                      : <span className="muted">—</span>}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="subsection-heading"><h3>条件树</h3></div>
        <ConditionTree node={detail.condition_tree} depth={0} />

        <div className="subsection-heading"><h3>排名与数量</h3></div>
        {detail.ranking.mode === "sort" ? (
          <p>
            多字段排序：
            {detail.ranking.fields.map((field) => `${field.input.binding_id} ${field.direction === "desc" ? "降序" : "升序"}`).join("，")}
            <span className="muted">（instrument_id 升序作为稳定决胜）</span>
          </p>
        ) : (
          <div className="table-scroll">
            <table className="data-table compact-table">
              <caption className="sr-only">评分分量</caption>
              <thead><tr><th scope="col">分量</th><th scope="col">权重</th><th scope="col">方向</th></tr></thead>
              <tbody>
                {detail.ranking.components.map((component) => (
                  <tr key={component.input.binding_id}>
                    <th scope="row" className="mono">{component.input.binding_id}</th>
                    <td className="mono">{component.weight}</td>
                    <td>{component.direction === "larger_is_better" ? "越大越好" : "越小越好"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <p>{detail.selection.mode === "all" ? "选中全部合格标的。" : `选中前 ${detail.selection.n} 名；不足时返回全部合格标的。`}</p>
        <p className="muted">展示列：{(detail.display_columns ?? []).length ? (detail.display_columns ?? []).join(", ") : "未声明"}</p>
      </Section>

      <Section title="提交运行" description="运行会冻结绑定、母池与决策时点。提交先重新预检；预检不过的请求不会创建任务。">
        <RunLauncher screenerRef={{ id: detail.id, version: detail.version }} />
      </Section>

      <Link className="quiet-link" to="/jobs">查看后台任务 →</Link>
    </div>
  );
}

function ConditionTree({ node, depth }: { node: components["schemas"]["ScreenCondition"]; depth: number }) {
  return (
    <div className={`node-readonly depth-${Math.min(depth, 3)}`}>
      <div className="node-headline">
        <span className="soft-label">{labelOf(node.kind)}</span>
        <span className="mono">{node.node_id}</span>
      </div>
      <NodeSummary node={node} />
      {"children" in node && node.children.length > 0 ? node.children.map((child, index) => <ConditionTree key={`${child.node_id}-${index}`} node={child} depth={depth + 1} />) : null}
      {"child" in node && node.child ? <ConditionTree node={node.child} depth={depth + 1} /> : null}
    </div>
  );
}

function NodeSummary({ node }: { node: components["schemas"]["ScreenCondition"] }) {
  switch (node.kind) {
    case "compare":
      return <p className="mono">{node.input.binding_id} {operatorLabel(node.operator)} {describeValue(node.value)}</p>;
    case "range":
      return (
        <p className="mono">
          {node.input.binding_id} ∈ [{node.lower ? describeValue(node.lower) : "−∞"}, {node.upper ? describeValue(node.upper) : "+∞"}]
          <span className="muted">（{node.lower_inclusive ? "含" : "不含"}下界，{node.upper_inclusive ? "含" : "不含"}上界）</span>
        </p>
      );
    case "set": {
      const set = setConditionMembers(node);
      return <p className="mono">{node.input.binding_id} {set.negated ? "∉" : "∈"} {"{"}{set.members.map(describeValue).join(", ")}{"}"}</p>;
    }
    case "missing":
      return <p className="mono">{node.input.binding_id} {missingConditionIsMissing(node) ? "缺失" : "存在"}</p>;
    default:
      return null;
  }
}

function describeValue(value: components["schemas"]["Value"] | null | undefined): string {
  if (!value) return "—";
  if (value.value === null || value.value === undefined) return `（${value.missing_reason ?? "无值"}）`;
  return String(value.value);
}

function labelOf(kind: string): string {
  const labels: Record<string, string> = {
    all: "AND · 全部满足",
    any: "OR · 任一满足",
    not: "NOT · 取反",
    compare: "比较",
    range: "区间",
    set: "集合",
    missing: "缺失检测",
  };
  return labels[kind] ?? kind;
}

function operatorLabel(operator: string): string {
  const labels: Record<string, string> = { gt: ">", gte: "≥", lt: "<", lte: "≤", eq: "=", ne: "≠" };
  return labels[operator] ?? operator;
}

type RunDraft = {
  snapshotId: string;
  universeId: string;
  asOf: string;
  decisionTimezone: string;
  strictPit: string;
  requiredValuePolicy: components["schemas"]["ScreenRunCreate"]["required_value_policy"];
};

const initialRunDraft: RunDraft = {
  snapshotId: "",
  universeId: "",
  asOf: "",
  decisionTimezone: "Asia/Shanghai",
  strictPit: "false",
  requiredValuePolicy: "exclude_instrument",
};

function RunLauncher({ screenerRef }: { screenerRef: components["schemas"]["VersionRef"] }) {
  const snapshots = useSnapshots();
  const universes = useUniverses("");
  const preflight = usePreflightScreenRun();
  const start = useStartScreenRun();
  const [draft, setDraft] = useState<RunDraft>(initialRunDraft);
  const [formError, setFormError] = useState("");
  const [lastRequest, setLastRequest] = useState<components["schemas"]["ScreenRunCreate"] | null>(null);

  const snapshotItems = useMemo(() => snapshots.data?.pages.flatMap((page) => page.items) ?? [], [snapshots.data?.pages]);
  const universeItems = useMemo(() => universes.data?.pages.flatMap((page) => page.items) ?? [], [universes.data?.pages]);
  // A universe version is bound to one snapshot, so the choices are narrowed to
  // the selected snapshot instead of letting the server reject a mismatch.
  const eligibleUniverses = useMemo(
    () => universeItems.filter((universe) => !draft.snapshotId || universe.snapshot_id === draft.snapshotId),
    [universeItems, draft.snapshotId],
  );
  const pending = preflight.isPending || start.isPending;

  function build(): components["schemas"]["ScreenRunCreate"] | null {
    if (!draft.snapshotId) return invalid("请选择快照。", "run-snapshot");
    if (!draft.universeId) return invalid("请选择标的池版本。", "run-universe");
    if (!draft.asOf.trim()) return invalid("请填写决策时点 as_of。", "run-as-of");
    if (!draft.decisionTimezone.trim()) return invalid("请填写决策时区。", "run-timezone");
    const universe = universeItems.find((item) => item.id === draft.universeId);
    if (universe && universe.snapshot_id !== draft.snapshotId) {
      return invalid("标的池版本绑定的快照与所选快照不一致。", "run-universe");
    }
    return {
      screener_ref: screenerRef,
      snapshot_id: draft.snapshotId,
      // A universe id already names one immutable revision; its version pin is
      // the definition hash, so the run cannot drift from what was selected.
      universe_ref: { id: draft.universeId, version: universe?.definition_hash ?? "" },
      as_of: draft.asOf.trim(),
      decision_timezone: draft.decisionTimezone.trim(),
      strict_pit: draft.strictPit === "true",
      required_value_policy: draft.requiredValuePolicy,
    };
  }

  function invalid(message: string, focusId: string): null {
    setFormError(message);
    document.getElementById(focusId)?.focus();
    return null;
  }

  async function dispatch(action: "preflight" | "run", request: components["schemas"]["ScreenRunCreate"]) {
    setLastRequest(request);
    if (action === "preflight") preflight.reset();
    else start.reset();
    try {
      if (action === "preflight") await preflight.mutateAsync(request);
      else await start.mutateAsync(request);
    } catch {
      // The normalized mutation error renders below.
    }
  }

  function launch(action: "preflight" | "run") {
    setFormError("");
    const request = build();
    if (!request) return;
    void dispatch(action, request);
  }

  const failure = preflight.error ?? start.error;

  return (
    <form className="stack-form" noValidate onSubmit={(event) => { event.preventDefault(); launch("run"); }}>
      <div className="form-grid">
        <Field label="快照" id="run-snapshot" help="运行只读快照内的数据；快照发布时决定的严格性约束 as_of 可见性。">
          <AntSelect
            id="run-snapshot"
            value={draft.snapshotId || null}
            placeholder={snapshotItems.length ? "选择快照" : "暂无快照"}
            onChange={(value: string) => setDraft({ ...draft, snapshotId: value, universeId: "" })}
            options={snapshotItems.map((snapshot) => ({ value: snapshot.id, label: `${snapshot.name} · ${snapshot.id}` }))}
            aria-label="快照"
          />
        </Field>
        <Field label="标的池版本" id="run-universe" help="历史母池在该时点解析；版本 pin 必须等于该版本的 definition hash。">
          <AntSelect
            id="run-universe"
            value={draft.universeId || null}
            placeholder={eligibleUniverses.length ? "选择版本" : "该快照下暂无版本"}
            onChange={(value: string) => setDraft({ ...draft, universeId: value })}
            options={eligibleUniverses.map((universe) => ({ value: universe.id, label: `${universe.name} · ${universe.id} · ${universe.definition.kind === "static" ? "静态" : "历史规则"}` }))}
            aria-label="标的池版本"
          />
        </Field>
        <Field label="决策时点 as_of" id="run-as-of" help="带时区 ISO 8601；服务端按此时点判定可见数据。">
          <Input id="run-as-of" value={draft.asOf} onChange={(event) => setDraft({ ...draft, asOf: event.target.value })} placeholder="2026-01-15T00:00:00Z" />
        </Field>
        <Field label="决策时区" id="run-timezone" help="必须是不含糊的 IANA 时区名，不用浏览器本地时区。">
          <Input id="run-timezone" value={draft.decisionTimezone} onChange={(event) => setDraft({ ...draft, decisionTimezone: event.target.value })} placeholder="Asia/Shanghai" />
        </Field>
        <Field label="严格 PIT" id="run-strict-pit" help="要求严格 PIT 时，快照也必须是严格模式，否则服务端拒绝。">
          <AntSelect
            id="run-strict-pit"
            value={draft.strictPit}
            onChange={(value: string) => setDraft({ ...draft, strictPit: value })}
            options={[{ value: "false", label: "按快照策略" }, { value: "true", label: "要求严格 PIT" }]}
            aria-label="严格 PIT"
          />
        </Field>
        <Field label="必填值策略" id="run-policy" help="fail_run：任何缺失/不可解析的必填值都会让运行失败，而不是被排除。">
          <AntSelect
            id="run-policy"
            value={draft.requiredValuePolicy}
            onChange={(value: RunDraft["requiredValuePolicy"]) => setDraft({ ...draft, requiredValuePolicy: value })}
            options={[
              { value: "exclude_instrument", label: "排除该标的" },
              { value: "fail_run", label: "整个运行失败" },
            ]}
            aria-label="必填值策略"
          />
        </Field>
      </div>
      <label className="checkbox-item">
        <span className="muted">提交后运行绑定此快照、母池与时点，之后修改方案只会创建新版本，不会改变这次运行。</span>
      </label>

      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {failure ? <QueryError error={failure} onRetry={() => { if (lastRequest) void dispatch("run", lastRequest); }} /> : null}
      {preflight.data ? <PreflightResult preflight={preflight.data} /> : null}
      {start.data ? (
        <Alert
          type="success"
          showIcon
          title={`运行已提交：任务 ${start.data.id}（${start.data.state}）。`}
          description={<span>结果在运行发布前不可读；可在 <Link className="table-link" to="/jobs">任务中心</Link> 跟踪进度，发布后从运行列表进入结果。</span>}
        />
      ) : null}

      <div className="form-actions">
        <Button htmlType="button" loading={preflight.isPending} disabled={pending} onClick={() => launch("preflight")}>预检</Button>
        <Button type="primary" htmlType="submit" loading={start.isPending} disabled={pending}>提交运行</Button>
        <span className="muted">提交会重新预检；不通过的请求不会创建任务</span>
      </div>
    </form>
  );
}

function PreflightResult({ preflight }: { preflight: components["schemas"]["ScreenPreflight"] }) {
  return (
    <div className="preflight-result" role="status">
      {preflight.valid ? (
        <Alert type="success" showIcon title="预检通过：可以提交运行。" />
      ) : (
        <Alert type="warning" showIcon title={`预检发现 ${preflight.issues.length} 个问题，一次性全部返回。`} description={<IssueList issues={preflight.issues} />} />
      )}
      <div className="table-scroll">
        <table className="data-table compact-table">
          <caption className="sr-only">逐绑定覆盖率</caption>
          <thead><tr><th scope="col">binding</th><th scope="col">可用</th><th scope="col">原因</th></tr></thead>
          <tbody>
            {preflight.coverage.map((coverage) => (
              <tr key={coverage.binding_id}>
                <th scope="row" className="mono">{coverage.binding_id}</th>
                <td>{coverage.available ? "是" : "否"}</td>
                <td className="mono">{coverage.reason ?? <span className="muted">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted mono">
        预计扫描行数：{preflight.estimated_scan_rows ?? "—"} · 预计结果行数：{preflight.estimated_rows ?? "—"}
      </p>
    </div>
  );
}
