import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState, type FormEvent } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import type { components } from "../../api/schema";
import {
  useFactor,
  useFactors,
  usePreflightFactorRun,
  useRunFactor,
  useRunFactorAnalysis,
  useSnapshots,
  useUniverses,
} from "../../api/hooks";
import { EmptyState, Field, IssueList, LoadMore, PageHeader, QueryError, QueryLoading, SearchField, Section } from "../ui";

type RunDraft = {
  snapshotId: string;
  universeId: string;
  factorKey: string;
  asOf: string;
  windowFrom: string;
  params: string;
};

type AnalysisDraft = {
  rangeFrom: string;
  rangeTo: string;
  asOf: string;
  horizons: string;
  groups: string;
  minSamples: string;
  method: "pearson" | "spearman";
};

const initialRun: RunDraft = { snapshotId: "", universeId: "", factorKey: "", asOf: "", windowFrom: "", params: "" };
const initialAnalysis: AnalysisDraft = { rangeFrom: "", rangeTo: "", asOf: "", horizons: "1,5,10", groups: "5", minSamples: "20", method: "pearson" };

export function FactorsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const factors = useFactors(query);
  const snapshots = useSnapshots();
  const universes = useUniverses("");
  const factorItems = useMemo(() => factors.data?.pages.flatMap((page) => page.items) ?? [], [factors.data?.pages]);
  const snapshotItems = useMemo(() => snapshots.data?.pages.flatMap((page) => page.items) ?? [], [snapshots.data?.pages]);
  const universeItems = useMemo(() => universes.data?.pages.flatMap((page) => page.items) ?? [], [universes.data?.pages]);

  function setQuery(nextQuery: string) {
    const next = new URLSearchParams(searchParams);
    if (nextQuery) next.set("q", nextQuery);
    else next.delete("q");
    setSearchParams(next);
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="研究输入 / FACTOR CATALOG"
        title="因子库"
        description="因子是注册的 id + version 对，上线后不可变。计算是同步的：预检一次返回全部问题，运行后直接返回横截面。"
        action={<Link className="button-link secondary-link" to="/universes">查看标的池</Link>}
      />

      <div className="metric-grid" aria-label="因子库概览">
        <MetricBox label="已注册因子" value={factors.isPending ? "—" : String(factorItems.length)} detail={query ? "当前搜索结果" : "当前页已加载"} />
        <MetricBox label="内置因子" value={factors.isPending ? "—" : String(factorItems.filter((factor) => factor.kind === "builtin").length)} detail="Go 闭包实现" />
        <MetricBox label="表达式因子" value={factors.isPending ? "—" : String(factorItems.filter((factor) => factor.kind === "expression").length)} detail="受限 AST 白名单" />
        <MetricBox label="参数化因子" value={factors.isPending ? "—" : String(factorItems.filter((factor) => factor.params.length > 0).length)} detail="参数经规范化冻结" accent />
      </div>

      <Section title="因子目录" description="服务端分页；kind、输入数据集与依赖关系由注册表返回，前端不做推断。">
        <div className="table-toolbar">
          <SearchFieldAdapter value={query} onChange={setQuery} />
          <span className="result-count">{factorItems.length} 条已加载</span>
        </div>
        {factors.isPending ? <QueryLoading label="正在读取因子目录" /> : factors.isError ? <QueryError error={factors.error} onRetry={() => void factors.refetch()} /> : factorItems.length === 0 ? (
          <EmptyState title={query ? "没有匹配因子" : "注册表为空"} description={query ? "清除搜索条件后查看全部因子。" : "服务启动时会注册内置因子族；注册表为空说明研究服务未装配。"} />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <caption className="sr-only">因子目录</caption>
                <thead><tr><th scope="col">因子</th><th scope="col">类型</th><th scope="col">输出单位</th><th scope="col">参数</th><th scope="col">输入</th><th scope="col">依赖</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  {factorItems.map((factor) => {
                    const key = factorKey(factor);
                    return (
                      <tr key={key}>
                        <th scope="row"><span>{factor.title}</span><small className="mono block">{key}</small></th>
                        <td><span className="soft-label">{factor.kind === "builtin" ? "内置" : "表达式"}</span></td>
                        <td className="mono">{factor.output_unit}</td>
                        <td>{factor.params.length ? factor.params.map((param) => param.name).join(", ") : <span className="muted">无</span>}</td>
                        <td>{factor.inputs.length ? factor.inputs.map((input) => input.dataset).join(", ") : <span className="muted">无</span>}</td>
                        <td>{factor.dependencies.length ? factor.dependencies.map((dep) => `${dep.id}@${dep.version}`).join(", ") : <span className="muted">无</span>}</td>
                        <td><Link className="table-link" to={factorDetailHref(searchParams.toString(), factor.id, factor.version)}>查看详情</Link></td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <LoadMore hasMore={Boolean(factors.hasNextPage)} loading={factors.isFetchingNextPage} onClick={() => void factors.fetchNextPage()} />
          </>
        )}
      </Section>

      <Section title="因子详情" description="读取单个已注册版本的能力声明：参数 schema、输入要求与依赖图。选中版本保存在 URL，可直接分享或刷新恢复。">
        <FactorDetail />
      </Section>

      <Section title="运行因子" description="[window_from, as_of) 为半开输入窗口；可用性遵循视图的 available_at ≤ as_of。预检不发散副作用，运行是同步的。">
        <RunPanel snapshots={snapshotItems} universes={universeItems} factors={factorItems} />
      </Section>

      <Section title="因子证据分析" description="标签由服务端从收盘价推导，只用于评估，永不作为因子输入；结果密封为 canonical artifact 后入库。">
        <AnalysisPanel snapshots={snapshotItems} universes={universeItems} factors={factorItems} />
      </Section>

      <p className="page-footnote">因子计算不会读取未来数据：标签只在分析阶段显式注入，因子视图不暴露标签读取。</p>
      <Link className="quiet-link" to="/jobs">查看后台任务 →</Link>
    </div>
  );
}

function FactorDetail() {
  // The selected version lives in the URL (/factors/:id?version=…), so a
  // dependency back-link and a shared address both land on the same view.
  const params = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const id = params.id ?? "";
  const version = searchParams.get("version") ?? "";
  const ref = useMemo(() => (id && version ? { id, version } : undefined), [id, version]);
  const factor = useFactor(ref);
  if (!id) {
    return <EmptyState title="尚未选择因子" description="在因子目录中选择一个版本，查看它的参数、输入与依赖。" />;
  }
  if (!version) {
    return <EmptyState title="缺少版本参数" description="因子详情必须带有 version 查询参数；请从因子目录进入。" />;
  }
  if (factor.isPending) return <QueryLoading label="正在读取因子详情" />;
  if (factor.isError) return <QueryError error={factor.error} onRetry={() => void factor.refetch()} />;
  if (!factor.data) return null;
  const detail = factor.data;
  return (
    <div className="detail-panel">
      <div className="detail-heading">
        <div><strong>{detail.title}</strong><small className="mono block">{factorKey(detail)}</small></div>
        <span className="soft-label">{detail.kind === "builtin" ? "内置" : "表达式"}</span>
      </div>
      <dl className="detail-grid">
        <div><dt>输出单位</dt><dd className="mono">{detail.output_unit}</dd></div>
        <div><dt>资产类别</dt><dd>{detail.asset_classes.join(", ") || "未声明"}</dd></div>
        <div><dt>依赖</dt><dd>{detail.dependencies.length ? detail.dependencies.map((dep) => (
          <Link key={`${dep.id}@${dep.version}`} className="table-link" to={factorDetailHref(searchParams.toString(), dep.id, dep.version)}>{dep.id}@{dep.version}</Link>
        )) : "无"}</dd></div>
      </dl>
      <div className="subsection-heading"><h3>参数</h3></div>
      {detail.params.length === 0 ? <p className="muted">该因子不接收参数。</p> : (
        <div className="table-scroll">
          <table className="data-table compact-table">
            <caption className="sr-only">因子参数</caption>
            <thead><tr><th scope="col">参数</th><th scope="col">类型</th><th scope="col">必填</th><th scope="col">默认</th><th scope="col">范围</th></tr></thead>
            <tbody>
              {detail.params.map((param) => (
                <tr key={param.name}>
                  <th scope="row" className="mono">{param.name}</th>
                  <td>{param.type}</td>
                  <td>{param.required ? "是" : "否"}</td>
                  <td className="mono">{param.default === undefined || param.default === null ? <span className="muted">无</span> : String(param.default)}</td>
                  <td className="mono">{paramRange(param)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="subsection-heading"><h3>输入要求</h3></div>
      {detail.inputs.length === 0 ? <p className="muted">该因子不声明外部输入。</p> : (
        <div className="table-scroll">
          <table className="data-table compact-table">
            <caption className="sr-only">因子输入</caption>
            <thead><tr><th scope="col">名称</th><th scope="col">数据集</th><th scope="col">字段</th><th scope="col">频率</th><th scope="col">回看</th><th scope="col">单位</th><th scope="col">PIT</th></tr></thead>
            <tbody>
              {detail.inputs.map((input) => (
                <tr key={input.name}>
                  <th scope="row" className="mono">{input.name}</th>
                  <td className="mono">{input.dataset}</td>
                  <td className="mono">{input.field}</td>
                  <td>{input.frequency}</td>
                  <td className="mono">{input.lookback === 0 ? "最新值" : String(input.lookback)}</td>
                  <td className="mono">{input.unit}</td>
                  <td>{input.pit ? "要求" : "不要求"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function RunPanel({ snapshots, universes, factors }: {
  snapshots: components["schemas"]["Snapshot"][];
  universes: components["schemas"]["Universe"][];
  factors: components["schemas"]["Factor"][];
}) {
  const [draft, setDraft] = useState(initialRun);
  const [formError, setFormError] = useState("");
  const preflight = usePreflightFactorRun();
  const run = useRunFactor();
  // The exact request that failed, so 重试 resends it verbatim.
  const [lastRequest, setLastRequest] = useState<components["schemas"]["FactorRunRequest"] | null>(null);
  const pending = preflight.isPending || run.isPending;

  function build(): components["schemas"]["FactorRunRequest"] | null {
    if (!draft.snapshotId) return invalid("请选择快照。", "run-snapshot");
    if (!draft.universeId) return invalid("请选择标的池版本。", "run-universe");
    const ref = parseFactorKey(draft.factorKey);
    if (!ref) return invalid("请选择因子版本。", "run-factor");
    if (!draft.asOf.trim()) return invalid("请填写决策时点 as_of。", "run-as-of");
    if (!draft.windowFrom.trim()) return invalid("请填写输入窗口起点 window_from。", "run-window-from");
    let params: Record<string, unknown> | undefined;
    if (draft.params.trim()) {
      let parsed: unknown;
      try {
        parsed = JSON.parse(draft.params);
      } catch {
        return invalid("参数必须是合法 JSON 对象。", "run-params");
      }
      if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return invalid("参数必须是 JSON 对象。", "run-params");
      params = parsed as Record<string, unknown>;
    }
    return {
      snapshot_id: draft.snapshotId,
      universe_id: draft.universeId,
      factor_ref: ref,
      as_of: draft.asOf.trim(),
      window_from: draft.windowFrom.trim(),
      ...(params ? { params } : {}),
    };
  }

  function invalid(message: string, focusId: string): null {
    setFormError(message);
    document.getElementById(focusId)?.focus();
    return null;
  }

  async function dispatch(action: "preflight" | "run", request: components["schemas"]["FactorRunRequest"]) {
    setLastRequest(request);
    try {
      if (action === "preflight") await preflight.mutateAsync(request);
      else await run.mutateAsync(request);
    } catch {
      // The normalized mutation error is rendered below.
    }
  }

  // Both buttons share one build/validate step; 重试 replays lastRequest so a
  // failure is reproducible without touching the form again.
  function launch(action: "preflight" | "run") {
    setFormError("");
    const request = build();
    if (!request) return;
    // Clear the other action so a stale error never sits next to a fresh result.
    if (action === "run") preflight.reset();
    else run.reset();
    void dispatch(action, request);
  }

  const failure = preflight.error ?? run.error;

  return (
    <form className="stack-form" noValidate onSubmit={(event) => { event.preventDefault(); launch("run"); }}>
      <div className="form-grid">
        <Field label="快照" id="run-snapshot" help="必须与所选标的池版本绑定的快照一致，否则服务端拒绝。">
          <AntSelect id="run-snapshot" value={draft.snapshotId || null} placeholder={snapshots.length ? "选择快照" : "暂无快照"} onChange={(value: string) => setDraft({ ...draft, snapshotId: value })} options={snapshots.map((snapshot) => ({ value: snapshot.id, label: `${snapshot.name} · ${snapshot.id}` }))} aria-label="快照" />
        </Field>
        <Field label="标的池版本" id="run-universe">
          <AntSelect id="run-universe" value={draft.universeId || null} placeholder={universes.length ? "选择版本" : "暂无版本"} onChange={(value: string) => setDraft({ ...draft, universeId: value })} options={universes.map((universe) => ({ value: universe.id, label: `${universe.name} · ${universe.id}` }))} aria-label="标的池版本" />
        </Field>
        <Field label="因子版本" id="run-factor">
          <AntSelect id="run-factor" value={draft.factorKey || null} placeholder={factors.length ? "选择因子" : "暂无因子"} onChange={(value: string) => setDraft({ ...draft, factorKey: value })} options={factors.map((factor) => ({ value: factorKey(factor), label: `${factor.title} · ${factorKey(factor)}` }))} aria-label="因子版本" />
        </Field>
        <Field label="决策时点 as_of" id="run-as-of" help="带时区 ISO 8601。"><Input id="run-as-of" value={draft.asOf} onChange={(event) => setDraft({ ...draft, asOf: event.target.value })} placeholder="2026-01-15T00:00:00Z" /></Field>
        <Field label="窗口起点 window_from" id="run-window-from" help="半开窗口 [window_from, as_of)。"><Input id="run-window-from" value={draft.windowFrom} onChange={(event) => setDraft({ ...draft, windowFrom: event.target.value })} placeholder="2025-11-01T00:00:00Z" /></Field>
      </div>
      <Field label="参数 JSON" id="run-params" help="按因子参数 schema 填写；留空表示使用声明的默认值。">
        <Input.TextArea id="run-params" value={draft.params} onChange={(event) => setDraft({ ...draft, params: event.target.value })} rows={3} />
      </Field>
      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {failure ? <QueryError error={failure} onRetry={() => { if (lastRequest) void dispatch("run", lastRequest); }} /> : null}
      {preflight.data ? <PreflightResult preflight={preflight.data} /> : null}
      {run.data ? <FrameResult frame={run.data} /> : null}
      <div className="form-actions">
        <Button htmlType="button" loading={preflight.isPending} disabled={pending} onClick={() => launch("preflight")}>预检</Button>
        <Button type="primary" htmlType="submit" loading={run.isPending} disabled={pending}>运行因子</Button>
        <span className="muted">同步返回横截面，不创建后台任务</span>
      </div>
    </form>
  );
}

function PreflightResult({ preflight }: { preflight: components["schemas"]["Preflight"] }) {
  return (
    <div className="preflight-result" role="status">
      {preflight.valid ? <Alert type="success" showIcon title="预检通过：可以运行该因子。" /> : <Alert type="warning" showIcon title={`预检发现 ${preflight.issues.length} 个问题，一次性全部返回。`} description={<IssueList issues={preflight.issues} />} />}
    </div>
  );
}

function FrameResult({ frame }: { frame: components["schemas"]["FactorRunResult"] }) {
  const missing = frame.total - frame.covered;
  return (
    <div className="frame-result" role="status">
      <Alert
        type={missing === 0 ? "success" : "info"}
        showIcon
        title={`横截面已计算：覆盖 ${frame.covered} / ${frame.total}（as_of ${frame.as_of}）。`}
        description={missing > 0 ? <span>缺失成员按原因单独列出，覆盖率不做补零。</span> : <span>所有成员都有值。</span>}
      />
      <div className="table-scroll">
        <table className="data-table compact-table">
          <caption className="sr-only">因子横截面</caption>
          <thead><tr><th scope="col">标的</th><th scope="col">值</th><th scope="col">缺失原因</th></tr></thead>
          <tbody>
            {frame.members.map((member) => (
              <tr key={member.instrument_id}>
                <th scope="row" className="mono">{member.instrument_id}</th>
                <td className="mono">{member.value ?? <span className="muted">—</span>}</td>
                <td>{member.missing_reason ?? <span className="muted">—</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function AnalysisPanel({ snapshots, universes, factors }: {
  snapshots: components["schemas"]["Snapshot"][];
  universes: components["schemas"]["Universe"][];
  factors: components["schemas"]["Factor"][];
}) {
  const [draft, setDraft] = useState(initialAnalysis);
  const [selection, setSelection] = useState({ snapshotId: "", universeId: "", factorKey: "" });
  const [formError, setFormError] = useState("");
  const mutation = useRunFactorAnalysis();
  const [lastRequest, setLastRequest] = useState<components["schemas"]["FactorAnalysisCreate"] | null>(null);

  async function dispatch(request: components["schemas"]["FactorAnalysisCreate"]) {
    setLastRequest(request);
    try {
      await mutation.mutateAsync(request);
    } catch {
      // The normalized mutation error is rendered below.
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    const request = buildAnalysis(selection, draft, setFormError);
    if (!request) return;
    void dispatch(request);
  }

  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <div className="form-grid">
        <Field label="分析快照" id="analysis-snapshot">
          <AntSelect id="analysis-snapshot" value={selection.snapshotId || null} placeholder={snapshots.length ? "选择快照" : "暂无快照"} onChange={(value: string) => setSelection({ ...selection, snapshotId: value })} options={snapshots.map((snapshot) => ({ value: snapshot.id, label: `${snapshot.name} · ${snapshot.id}` }))} aria-label="分析快照" />
        </Field>
        <Field label="分析标的池版本" id="analysis-universe">
          <AntSelect id="analysis-universe" value={selection.universeId || null} placeholder={universes.length ? "选择版本" : "暂无版本"} onChange={(value: string) => setSelection({ ...selection, universeId: value })} options={universes.map((universe) => ({ value: universe.id, label: `${universe.name} · ${universe.id}` }))} aria-label="分析标的池版本" />
        </Field>
        <Field label="分析因子版本" id="analysis-factor">
          <AntSelect id="analysis-factor" value={selection.factorKey || null} placeholder={factors.length ? "选择因子" : "暂无因子"} onChange={(value: string) => setSelection({ ...selection, factorKey: value })} options={factors.map((factor) => ({ value: factorKey(factor), label: `${factor.title} · ${factorKey(factor)}` }))} aria-label="分析因子版本" />
        </Field>
        <Field label="区间 from（含）" id="analysis-range-from" help="研究区间的左界，半开 [from, to)。"><Input id="analysis-range-from" value={draft.rangeFrom} onChange={(event) => setDraft({ ...draft, rangeFrom: event.target.value })} placeholder="2025-09-01T00:00:00Z" /></Field>
        <Field label="区间 to（不含）" id="analysis-range-to"><Input id="analysis-range-to" value={draft.rangeTo} onChange={(event) => setDraft({ ...draft, rangeTo: event.target.value })} placeholder="2026-01-01T00:00:00Z" /></Field>
        <Field label="研究时点 as_of" id="analysis-as-of" help="必须是研究当下，不能早于区间右界。"><Input id="analysis-as-of" value={draft.asOf} onChange={(event) => setDraft({ ...draft, asOf: event.target.value })} placeholder="2026-01-15T00:00:00Z" /></Field>
        <Field label="持有期 horizons" id="analysis-horizons" help="以交易日计，逗号分隔，例如 1,5,10。"><Input id="analysis-horizons" value={draft.horizons} onChange={(event) => setDraft({ ...draft, horizons: event.target.value })} /></Field>
        <Field label="分组数 groups" id="analysis-groups" help="2 至 10 组，第 1 组为因子值最低。"><Input id="analysis-groups" value={draft.groups} onChange={(event) => setDraft({ ...draft, groups: event.target.value })} /></Field>
        <Field label="最小样本数 min_samples" id="analysis-min-samples" help="不足则返回 null 与原因，不写 0。"><Input id="analysis-min-samples" value={draft.minSamples} onChange={(event) => setDraft({ ...draft, minSamples: event.target.value })} /></Field>
        <Field label="相关方法" id="analysis-method">
          <AntSelect id="analysis-method" value={draft.method} onChange={(value: AnalysisDraft["method"]) => setDraft({ ...draft, method: value })} options={[{ value: "pearson", label: "Pearson" }, { value: "spearman", label: "Spearman（Rank IC）" }]} aria-label="相关方法" />
        </Field>
      </div>
      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {mutation.isError ? <QueryError error={mutation.error} onRetry={() => { if (lastRequest) void dispatch(lastRequest); }} /> : null}
      {mutation.data ? <AnalysisResult result={mutation.data} /> : null}
      <div className="form-actions"><Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={mutation.isPending}>运行分析</Button><span className="muted">结果密封为 canonical artifact</span></div>
    </form>
  );
}

function AnalysisResult({ result }: { result: components["schemas"]["FactorAnalysisResult"] }) {
  const summary = result.summary;
  return (
    <div className="analysis-result" role="status">
      <Alert
        type="success"
        showIcon
        title={`分析完成：${summary.dates} 个横截面日期，覆盖 ${summary.coverage.covered} / ${summary.coverage.total}。`}
        description={<span className="mono">artifact {result.artifact.id} · checksum {result.artifact.checksum}</span>}
      />
      <div className="table-scroll">
        <table className="data-table compact-table">
          <caption className="sr-only">按持有期的因子证据</caption>
          <thead><tr><th scope="col">持有期</th><th scope="col">样本对</th><th scope="col">IC 均值</th><th scope="col">Rank IC 均值</th><th scope="col">IC IR</th><th scope="col">正 IC 占比</th></tr></thead>
          <tbody>
            {summary.horizons.map((horizon) => (
              <tr key={horizon.horizon}>
                <th scope="row" className="mono">{horizon.horizon} 日</th>
                <td className="mono">{horizon.pairs}</td>
                <td className="mono">{formatStat(horizon.ic.mean)}</td>
                <td className="mono">{formatStat(horizon.rank_ic.mean)}</td>
                <td className="mono">{formatStat(horizon.ic.ir)}</td>
                <td className="mono">{formatStat(horizon.ic.positive_rate)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted">{result.evidence_note}</p>
      <p className="mono muted">标签口径：{result.config.label_price} / {result.config.label_entry} / {result.config.label_cost}</p>
    </div>
  );
}

function buildAnalysis(selection: { snapshotId: string; universeId: string; factorKey: string }, draft: AnalysisDraft, fail: (message: string) => void): components["schemas"]["FactorAnalysisCreate"] | null {
  if (!selection.snapshotId) return failAnalysis(fail, "请选择快照。", "analysis-snapshot");
  if (!selection.universeId) return failAnalysis(fail, "请选择标的池版本。", "analysis-universe");
  const ref = parseFactorKey(selection.factorKey);
  if (!ref) return failAnalysis(fail, "请选择因子版本。", "analysis-factor");
  if (!draft.rangeFrom.trim() || !draft.rangeTo.trim()) return failAnalysis(fail, "请填写分析区间 [from, to)。", "analysis-range-from");
  if (!draft.asOf.trim()) return failAnalysis(fail, "请填写研究时点 as_of。", "analysis-as-of");
  const horizons = draft.horizons.split(",").map((value) => Number.parseInt(value.trim(), 10)).filter((value) => Number.isInteger(value) && value >= 1);
  if (horizons.length === 0) return failAnalysis(fail, "至少填写一个正整数的持有期。", "analysis-horizons");
  const groups = Number.parseInt(draft.groups.trim(), 10);
  if (!Number.isInteger(groups) || groups < 2 || groups > 10) return failAnalysis(fail, "分组数必须是 2 至 10 的整数。", "analysis-groups");
  const minSamples = Number.parseInt(draft.minSamples.trim(), 10);
  if (!Number.isInteger(minSamples) || minSamples < 2) return failAnalysis(fail, "最小样本数必须是不小于 2 的整数。", "analysis-min-samples");
  return {
    snapshot_id: selection.snapshotId,
    universe_id: selection.universeId,
    factor_ref: ref,
    range: { from: draft.rangeFrom.trim(), to: draft.rangeTo.trim() },
    as_of: draft.asOf.trim(),
    horizons,
    groups,
    min_samples: minSamples,
    method: draft.method,
  };
}

function failAnalysis(fail: (message: string) => void, message: string, focusId: string): null {
  fail(message);
  document.getElementById(focusId)?.focus();
  return null;
}

function formatStat(stat: components["schemas"]["Stat"]): string {
  if (stat.value === null || stat.value === undefined) return stat.reason ? `— (${stat.reason})` : "—";
  return stat.value.toFixed(4);
}

function paramRange(param: components["schemas"]["FactorParam"]): string {
  const parts: string[] = [];
  if (param.min !== undefined && param.min !== null) parts.push(`≥ ${param.min}`);
  if (param.max !== undefined && param.max !== null) parts.push(`≤ ${param.max}`);
  if (param.enum?.length) parts.push(param.enum.join(" | "));
  return parts.length ? parts.join("，") : "—";
}

/** The registry keys factors by (id, version); the composite key travels in one select value. */
function factorKey(factor: components["schemas"]["Factor"]): string {
  return `${factor.id}@${factor.version}`;
}

/** Detail is a URL address — /factors/:id?version=… — that keeps the catalog search. */
function factorDetailHref(currentSearch: string, id: string, version: string): string {
  const next = new URLSearchParams(currentSearch);
  next.set("version", version);
  return `/factors/${encodeURIComponent(id)}?${next.toString()}`;
}

function parseFactorKey(key: string): components["schemas"]["VersionRef"] | undefined {
  if (!key) return undefined;
  const at = key.lastIndexOf("@");
  if (at <= 0) return undefined;
  return { id: key.slice(0, at), version: key.slice(at + 1) };
}

function MetricBox({ label, value, detail, accent = false }: { label: string; value: string; detail: string; accent?: boolean }) {
  return <div className={`metric-box${accent ? " accent" : ""}`}><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>;
}

function SearchFieldAdapter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <SearchField value={value} onChange={onChange} placeholder="按名称或 ID 搜索" />;
}
