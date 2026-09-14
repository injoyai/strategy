import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import type { components } from "../../api/schema";
import {
  useSaveScreenUniverse,
  useScreenExplanation,
  useScreenRun,
  useScreenRows,
  useUniverse,
} from "../../api/hooks";
import { EmptyState, Field, LoadMore, PageHeader, QueryError, QueryLoading, Section, formatDateTime } from "../ui";
import { screenerHref } from "./ScreenersPage";

/**
 * One screening run: the frozen configuration it was computed from, its
 * published summary and rows, per-instrument explanations, and the pool that can
 * be saved from its complete selection. Nothing here can change a run — a
 * different snapshot, pool or time is a different run.
 */
export function ScreenRunPage() {
  const params = useParams<{ id: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const runId = params.id ?? "";
  const stateFilter = (searchParams.get("state") ?? "") as "" | "selected" | "excluded";
  const run = useScreenRun(runId || undefined);
  const published = Boolean(run.data && run.data.summary !== null);

  function setStateFilter(next: string) {
    const next_Params = new URLSearchParams(searchParams);
    if (next) next_Params.set("state", next);
    else next_Params.delete("state");
    setSearchParams(next_Params);
  }

  if (!runId) {
    return <EmptyState title="缺少运行 ID" description="运行详情需要 URL 中的 run id；请从运行列表进入。" />;
  }
  if (run.isPending) return <QueryLoading label="正在读取运行" />;
  if (run.isError) return <QueryError error={run.error} onRetry={() => void run.refetch()} />;
  if (!run.data) return null;
  const record = run.data;

  const snapshotsHref = `/snapshots?focus=${encodeURIComponent(record.config.snapshot_id)}`;

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="选股 / SCREEN RUN"
        title={`运行 ${record.id}`}
        description="结果与配置一起冻结：发布后行、排名与解释都不会随之后的快照、母池或方案修改而变化。"
        action={
          <div className="header-actions">
            <Link className="button-link secondary-link" to={screenerHref(record.config.screener_ref.id, record.config.screener_ref.version)}>查看方案修订</Link>
            <Link className="button-link secondary-link" to="/screeners">返回方案目录</Link>
          </div>
        }
      />

      <Section title="冻结的配置" description="运行提交后这些引用不再变化；换快照或时点是新的一次运行。">
        <dl className="detail-grid">
          <div><dt>方案修订</dt><dd className="mono"><Link className="table-link" to={screenerHref(record.config.screener_ref.id, record.config.screener_ref.version)}>{record.config.screener_ref.id}@{record.config.screener_ref.version}</Link></dd></div>
          <div><dt>快照</dt><dd className="mono"><Link className="table-link" to={snapshotsHref}>{record.config.snapshot_id}</Link></dd></div>
          <div><dt>母池版本</dt><dd className="mono">{record.config.universe_ref.id}@{record.config.universe_ref.version}</dd></div>
          <div><dt>决策时点</dt><dd className="mono">{record.config.as_of}（{record.config.decision_timezone}）</dd></div>
          <div><dt>严格 PIT</dt><dd>{record.config.strict_pit ? "要求" : "按快照策略"}</dd></div>
          <div><dt>必填值策略</dt><dd>{record.config.required_value_policy === "fail_run" ? "整个运行失败" : "排除该标的"}</dd></div>
          <div><dt>引擎版本</dt><dd className="mono">{record.engine_version}</dd></div>
          <div><dt>评分策略</dt><dd className="mono">{record.scoring_policy_version}</dd></div>
          <div><dt>配置哈希</dt><dd className="mono truncate">{record.config_hash}</dd></div>
          <div><dt>快照哈希</dt><dd className="mono truncate">{record.snapshot_hash}</dd></div>
          <div><dt>创建时间</dt><dd>{formatDateTime(record.created_at)}</dd></div>
          <div><dt>任务</dt><dd><Link className="table-link" to={`/jobs?q=${encodeURIComponent(record.job_id)}`}>查看任务 {record.job_id}</Link></dd></div>
        </dl>
        <p className="muted">
          运行状态由任务 <span className="mono">{record.job_id}</span> 持有；
          <Link className="table-link" to={`/jobs?q=${encodeURIComponent(record.job_id)}`}>查看任务详情</Link>
          {" "}确认它是已完成、失败还是已取消。
        </p>
      </Section>

      {record.summary === null ? (
        <Section title="结果" description="运行尚未发布结果。">
          <Alert
            type="info"
            showIcon
            title="结果尚未发布"
            description={<span>行与解释在运行发布前不可读（服务端返回 409 result_not_ready）。请在 <Link className="table-link" to={`/jobs?q=${encodeURIComponent(record.job_id)}`}>任务中心</Link> 跟踪进度。</span>}
          />
        </Section>
      ) : (
        <>
          <Section title="汇总" description="阶段互斥且守恒：母池 = false + unknown + true；true = 排名不足 + 可排名；可排名 = 入选 + 未入选。">
            <div className="metric-grid" aria-label="阶段汇总">
              <MetricBox label="母池" value={String(record.summary.population)} detail="运行时的母池成员数" />
              <MetricBox label="条件 false" value={String(record.summary.condition_false)} detail="明确不满足" />
              <MetricBox label="条件 unknown" value={String(record.summary.condition_unknown)} detail="数据不足，不当作 false" />
              <MetricBox label="排名数据不足" value={String(record.summary.rank_insufficient)} detail="条件通过但无法排名" />
              <MetricBox label="可排名" value={String(record.summary.rankable)} detail="进入排序/评分的标的" />
              <MetricBox label="入选" value={String(record.summary.selected)} detail="本次选中的标的" accent />
              <MetricBox label="未入选" value={String(record.summary.not_selected)} detail="合格但未入选" />
              <MetricBox label="空结果原因" value={record.summary.empty_reason ?? "—"} detail="不自动放宽条件" />
            </div>
            <SavePoolPanel runId={record.id} selected={record.summary.selected} />
          </Section>

          <Section title="结果行" description="正式 rank 升序，instrument_id 作为稳定决胜；排除行的 rank 为空。UI 过滤不改动正式名单。">
            <div className="table-toolbar">
              <Field label="阶段过滤" id="row-state-filter" help="过滤只影响当前列表，不影响正式名单与导出范围。">
                <AntSelect
                  id="row-state-filter"
                  value={stateFilter || null}
                  allowClear
                  placeholder="全部行"
                  onChange={(value: string | undefined) => setStateFilter(value ?? "")}
                  options={[{ value: "selected", label: "仅入选" }, { value: "excluded", label: "仅未入选" }]}
                  aria-label="阶段过滤"
                />
              </Field>
            </div>
            <RowsTable runId={record.id} stateFilter={stateFilter} enabled={published} />
          </Section>
        </>
      )}

      <p className="page-footnote">排名与名单以服务端返回为准；页面上的搜索、分页与展示排序只在当前视图内生效。</p>
      <Link className="quiet-link" to="/jobs">查看后台任务 →</Link>
    </div>
  );
}

function MetricBox({ label, value, detail, accent = false }: { label: string; value: string; detail: string; accent?: boolean }) {
  return <div className={`metric-box${accent ? " accent" : ""}`}><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>;
}

function RowsTable({ runId, stateFilter, enabled }: { runId: string; stateFilter: "" | "selected" | "excluded"; enabled: boolean }) {
  const rows = useScreenRows(runId, stateFilter, enabled);
  const items = useMemo(() => rows.data?.pages.flatMap((page) => page.items) ?? [], [rows.data?.pages]);
  const columns = rows.data?.pages[0]?.columns ?? [];
  const [explainedInstrument, setExplainedInstrument] = useState<string>("");

  if (!enabled) {
    return <EmptyState title="结果尚未发布" description="发布后这里显示冻结的结果行与解释。" />;
  }
  if (rows.isPending) return <QueryLoading label="正在读取结果行" />;
  if (rows.isError) return <QueryError error={rows.error} onRetry={() => void rows.refetch()} />;
  if (items.length === 0) {
    return <EmptyState title="没有符合条件的行" description="0 个结果是合法成功态：母池为空、条件不满足或排名数据不足，服务端会在汇总里给出原因。" />;
  }

  return (
    <div className="stack-form">
      <div className="table-scroll">
        <table className="data-table">
          <caption className="sr-only">结果行</caption>
          <thead>
            <tr>
              <th scope="col">排名</th>
              <th scope="col">标的</th>
              <th scope="col">入选</th>
              <th scope="col">评分</th>
              <th scope="col">阶段</th>
              {columns.map((column) => (
                <th key={column.name} scope="col">{column.name}<small className="block muted">{column.unit || "未声明单位"}{column.nullable ? " · 可缺失" : ""}</small></th>
              ))}
              <th scope="col">解释</th>
            </tr>
          </thead>
          <tbody>
            {items.map((row) => (
              <tr key={row.instrument_id}>
                <td className="mono">{row.rank ?? <span className="muted">—</span>}</td>
                <th scope="row" className="mono">{row.instrument_id}</th>
                <td>{row.selected ? "是" : "否"}</td>
                <td className="mono">{row.score ?? <span className="muted">—</span>}</td>
                <td>{stageLabel(row.reason)}</td>
                {columns.map((column) => {
                  const value = row.values[column.name];
                  return (
                    <td key={`${row.instrument_id}-${column.name}`} className="mono">
                      {value === undefined || value.missing_reason
                        ? <span className="muted" title={value?.missing_reason ?? "missing_value"}>—（{value?.missing_reason ?? "missing_value"}）</span>
                        : value.value}
                    </td>
                  );
                })}
                <td>
                  <Button type="link" onClick={() => setExplainedInstrument(row.instrument_id)} aria-label={`查看 ${row.instrument_id} 的解释`}>查看解释</Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <LoadMore hasMore={Boolean(rows.hasNextPage)} loading={rows.isFetchingNextPage} onClick={() => void rows.fetchNextPage()} />
      {explainedInstrument ? <ExplanationPanel runId={runId} instrumentId={explainedInstrument} onClose={() => setExplainedInstrument("")} /> : null}
      <p className="muted">上表为服务端分页的冻结结果；页内搜索/排序不改变正式排名与入选集合。</p>
    </div>
  );
}

function ExplanationPanel({ runId, instrumentId, onClose }: { runId: string; instrumentId: string; onClose: () => void }) {
  const explanation = useScreenExplanation(runId, instrumentId);
  return (
    <Section
      title={`解释 · ${instrumentId}`}
      description="逐节点证据来自冻结数据，不是当前值；unknown 是独立的真值，不会被 NOT 变成 true。"
      action={<Button type="link" onClick={onClose}>关闭</Button>}
      className="explanation-panel"
    >
      {explanation.isPending ? <QueryLoading label="正在读取解释" /> : explanation.isError ? <QueryError error={explanation.error} onRetry={() => void explanation.refetch()} /> : explanation.data ? (
        <div className="stack-form">
          <p>阶段：<span className="soft-label">{stageLabel(explanation.data.stage)}</span></p>
          <NodeEvidence node={explanation.data.nodes} depth={0} />
          {explanation.data.score.length > 0 ? (
            <div className="table-scroll">
              <table className="data-table compact-table">
                <caption className="sr-only">评分分量</caption>
                <thead><tr><th scope="col">分量</th><th scope="col">原始值</th><th scope="col">百分位</th><th scope="col">权重</th><th scope="col">贡献</th></tr></thead>
                <tbody>
                  {explanation.data.score.map((component) => (
                    <tr key={component.binding_id}>
                      <th scope="row" className="mono">{component.binding_id}</th>
                      <td className="mono">{component.raw_value?.value ?? <span className="muted">—（{component.raw_value?.missing_reason ?? "missing_value"}）</span>}</td>
                      <td className="mono">{component.percentile ?? "—"}</td>
                      <td className="mono">{component.weight}</td>
                      <td className="mono">{component.contribution}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : <p className="muted">排序模式下没有评分分量。</p>}
        </div>
      ) : null}
    </Section>
  );
}

/** The explanation is one root node plus its children: the per-node truth is the
 * frozen evidence, so a row is explained without reading current data. */
function NodeEvidence({ node, depth }: { node: components["schemas"]["ScreenNodeEvaluation"]; depth: number }) {
  return (
    <div className={`node-readonly depth-${Math.min(depth, 3)}`}>
      <div className="node-headline">
        <span className={`issue-dot ${node.truth === "true" ? "issue-info" : node.truth === "false" ? "issue-error" : "issue-warning"}`} aria-hidden="true" />
        <span className="mono">{node.node_id}</span>
        <span className="soft-label">{node.truth === "true" ? "true" : node.truth === "false" ? "false" : "unknown"}</span>
        {node.input ? <span className="mono muted">{node.input.binding_id}</span> : null}
        {node.threshold ? <span className="mono muted">阈值 {node.threshold.value ?? node.threshold.missing_reason}</span> : null}
      </div>
      {node.missing_reason ? <p className="muted">缺失原因：{node.missing_reason}</p> : null}
      {node.children.map((child, index) => <NodeEvidence key={`${child.node_id}-${index}`} node={child} depth={depth + 1} />)}
    </div>
  );
}

function SavePoolPanel({ runId, selected }: { runId: string; selected: number }) {
  const save = useSaveScreenUniverse();
  const [name, setName] = useState("");
  const [formError, setFormError] = useState("");
  const saved = save.data;
  const savedUniverse = useUniverse(saved?.id);

  function submit() {
    setFormError("");
    if (!name.trim()) {
      setFormError("请填写标的池名称。");
      document.getElementById("pool-name")?.focus();
      return;
    }
    save.mutate({ runId, body: { name: name.trim() } });
  }

  return (
    <div className="stack-form">
      <p className="muted">
        保存静态池使用本次运行的<strong>完整入选集合</strong>（{selected} 个标的），不是当前页面或过滤后的列表。
        池会带上本次运行的决策时点、数据哈希与质量限制。
      </p>
      {selected === 0 ? (
        <Alert type="warning" showIcon title="本次运行没有入选标的" description="空名单不能保存为标的池；请调整方案或时点后重新运行。" />
      ) : (
        <>
          <div className="form-grid">
            <Field label="标的池名称" id="pool-name">
              <Input id="pool-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：2026-01 质量动量池" />
            </Field>
          </div>
          {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
          {save.isError ? <QueryError error={save.error} onRetry={submit} actionLabel="重新保存" /> : null}
          {saved ? (
            <Alert
              type="success"
              showIcon
              title={`已保存标的池 ${saved.name}（${saved.id}）。`}
              description={
                <span>
                  来源运行 {saved.source?.screen_run_id ?? runId}，决策时点 {saved.source?.as_of ?? "—"}。
                  {savedUniverse.data ? ` 当前成员 ${universeMemberCount(savedUniverse.data)} 个。` : ""}
                </span>
              }
            />
          ) : null}
          <div className="form-actions">
            <Button type="primary" htmlType="button" loading={save.isPending} disabled={save.isPending} onClick={submit}>保存为静态标的池</Button>
            <span className="muted">该池只可用于决策时点不早于它的回测</span>
          </div>
        </>
      )}
    </div>
  );
}

function stageLabel(stage: string): string {
  const labels: Record<string, string> = {
    selected: "入选",
    condition_false: "条件不满足",
    condition_unknown: "条件 unknown",
    rank_insufficient: "排名数据不足",
    not_selected: "未入选",
  };
  return labels[stage] ?? stage;
}

/** A historical_rule universe has no member list of its own: its size is only
 * known by resolving it at a decision time. */
function universeMemberCount(universe: components["schemas"]["Universe"]): number {
  return universe.definition.kind === "static" ? universe.definition.members.length : 0;
}
