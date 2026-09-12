import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router";
import type { components } from "../../api/schema";
import {
  useBatches,
  useConnections,
  useDatasets,
  useSnapshots,
  useStartIngestion,
  useStartSnapshot,
} from "../../api/hooks";
import { EmptyState, LoadMore, PageHeader, QueryError, QueryLoading, SearchField, Section, formatDateTime } from "../ui";

type IngestionDraft = {
  connectionKey: string;
  dataset: string;
  frequency: string;
  instruments: string;
  from: string;
  to: string;
  mode: "incremental" | "backfill" | "force" | "";
  mapping: string;
  timezone: string;
  availabilityId: string;
  availabilityVersion: string;
};

type SnapshotDraft = {
  name: string;
  batchIds: string[];
  strictPit: "true" | "false" | "";
};

const initialIngestion: IngestionDraft = {
  connectionKey: "",
  dataset: "",
  frequency: "",
  instruments: "",
  from: "",
  to: "",
  mode: "",
  mapping: "",
  timezone: "",
  availabilityId: "",
  availabilityVersion: "",
};

const initialSnapshot: SnapshotDraft = { name: "", batchIds: [], strictPit: "" };

export function DataPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const datasets = useDatasets(query);
  const connections = useConnections("");
  const batches = useBatches();
  const snapshots = useSnapshots();
  const datasetItems = useMemo(() => datasets.data?.pages.flatMap((page) => page.items) ?? [], [datasets.data?.pages]);
  const connectionItems = useMemo(() => connections.data?.pages.flatMap((page) => page.items) ?? [], [connections.data?.pages]);
  const batchItems = useMemo(() => batches.data?.pages.flatMap((page) => page.items) ?? [], [batches.data?.pages]);
  const snapshotItems = useMemo(() => snapshots.data?.pages.flatMap((page) => page.items) ?? [], [snapshots.data?.pages]);

  function setQuery(nextQuery: string) {
    const next = new URLSearchParams(searchParams);
    if (nextQuery) next.set("q", nextQuery);
    else next.delete("q");
    setSearchParams(next);
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="研究输入 / DATA EVIDENCE"
        title="数据中心"
        description="从已注册连接发起更新，检查批次质量，再发布不可变快照。所有查询都必须绑定快照与 as_of。"
        action={<Link className="button-link secondary-link" to="/connections">管理数据源</Link>}
      />

      <div className="metric-grid" aria-label="数据中心概览">
        <MetricBox label="数据集" value={datasets.isPending ? "—" : String(datasetItems.length)} detail={query ? "当前搜索结果" : "当前页已加载"} />
        <MetricBox label="待检查批次" value={batches.isPending ? "—" : String(batchItems.filter((batch) => !batch.ready).length)} detail="以 API ready 字段为准" />
        <MetricBox label="可用快照" value={snapshots.isPending ? "—" : String(snapshotItems.length)} detail="不可变研究输入" />
        <MetricBox label="PIT 约束" value="必选" detail="available_at ≤ as_of" accent />
      </div>

      <Section title="发起数据更新" description="强制更新仍会调用上游并形成新的请求/批次证据；页面不会替你选择市场、频率、时区或费用口径。">
        {connections.isPending ? <QueryLoading label="正在读取可用连接" /> : connections.isError ? <QueryError error={connections.error} onRetry={() => void connections.refetch()} /> : <IngestionForm connections={connectionItems} />}
      </Section>

      <Section title="数据集目录" description="服务端分页，搜索条件保存在 URL；字段、单位和质量问题由 Dataset API 返回。">
        <div className="table-toolbar">
          <SearchFieldAdapter value={query} onChange={setQuery} />
          <span className="result-count">{datasetItems.length} 条已加载</span>
        </div>
        {datasets.isPending ? <QueryLoading label="正在读取数据集目录" /> : datasets.isError ? <QueryError error={datasets.error} onRetry={() => void datasets.refetch()} /> : datasetItems.length === 0 ? (
          <EmptyState title={query ? "没有匹配数据集" : "还没有数据集"} description={query ? "清除搜索条件后查看全部目录。" : "数据更新完成后，标准化数据集会出现在这里。"} />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <caption className="sr-only">数据集目录</caption>
                <thead><tr><th scope="col">数据集</th><th scope="col">Schema</th><th scope="col">覆盖范围</th><th scope="col">质量</th><th scope="col">字段</th></tr></thead>
                <tbody>
                  {datasetItems.map((dataset) => (
                    <tr key={dataset.id}>
                      <th scope="row"><span>{dataset.name}</span><small className="mono block">{dataset.id}</small></th>
                      <td className="mono">{dataset.schema_version}</td>
                      <td>{dataset.coverage ? `${dataset.coverage.from} → ${dataset.coverage.to}` : <span className="muted">未声明</span>}</td>
                      <td><QualityLabel issues={dataset.quality_issues} /></td>
                      <td>{dataset.fields.length} 个字段</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <LoadMore hasMore={Boolean(datasets.hasNextPage)} loading={datasets.isFetchingNextPage} onClick={() => void datasets.fetchNextPage()} />
          </>
        )}
      </Section>

      <div className="two-column-grid">
        <Section title="批次质量" description="先处理 error 级问题，再发布快照；warning/info 会保留在证据中。">
          {batches.isPending ? <QueryLoading label="正在读取批次" /> : batches.isError ? <QueryError error={batches.error} onRetry={() => void batches.refetch()} /> : batchItems.length === 0 ? (
            <EmptyState title="还没有批次" description="完成一次数据更新后，批次与质量问题会在这里出现。" />
          ) : (
            <>
              <div className="table-scroll">
                <table className="data-table compact-table">
                  <caption className="sr-only">批次质量列表</caption>
                  <thead><tr><th scope="col">批次</th><th scope="col">行数</th><th scope="col">状态</th><th scope="col">问题</th></tr></thead>
                  <tbody>
                    {batchItems.map((batch) => (
                      <tr key={batch.id}>
                        <th scope="row" className="mono">{batch.id}</th>
                        <td className="mono">{batch.row_count.toLocaleString("zh-CN")}</td>
                        <td><span className={`ready-label ${batch.ready ? "ready" : "not-ready"}`}>{batch.ready ? "可发布" : "待处理"}</span></td>
                        <td>{batch.issues.length ? <span className="issue-count">{batch.issues.length} 条</span> : <span className="muted">无</span>}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <LoadMore hasMore={Boolean(batches.hasNextPage)} loading={batches.isFetchingNextPage} onClick={() => void batches.fetchNextPage()} />
            </>
          )}
        </Section>

        <Section title="发布快照" description="快照绑定批次并保持不可变。严格 PIT 会拒绝缺少 published_at 的记录。">
          <SnapshotForm batches={batchItems} />
          <div className="subsection-heading"><h3>最近快照</h3><span className="result-count">{snapshotItems.length} 条已加载</span></div>
          {snapshots.isPending ? <QueryLoading label="正在读取快照" /> : snapshots.isError ? <QueryError error={snapshots.error} onRetry={() => void snapshots.refetch()} /> : snapshotItems.length === 0 ? (
            <EmptyState title="还没有已发布快照" description="质量合格的批次发布后，快照会成为可绑定的研究输入。" />
          ) : (
            <>
              <div className="snapshot-list">
                {snapshotItems.map((snapshot) => (
                  <article className="snapshot-row" key={snapshot.id}>
                    <div><strong>{snapshot.name}</strong><small className="mono block">{snapshot.id}</small></div>
                    <div><span className="soft-label">{snapshot.strict_pit ? "严格 PIT" : "非严格 PIT"}</span><small className="block">{formatDateTime(snapshot.created_at)}</small></div>
                    <QualityLabel issues={snapshot.quality_issues} />
                  </article>
                ))}
              </div>
              <LoadMore hasMore={Boolean(snapshots.hasNextPage)} loading={snapshots.isFetchingNextPage} onClick={() => void snapshots.fetchNextPage()} />
            </>
          )}
        </Section>
      </div>

      <p className="page-footnote">数据预览将在选择快照和历史时点后开放。前端不预加载未来数据，也不把 quality error 当作可用数据。</p>
      <Link className="quiet-link" to="/jobs">查看后台任务 →</Link>
    </div>
  );
}

function IngestionForm({ connections }: { connections: components["schemas"]["Connection"][] }) {
  const [draft, setDraft] = useState(initialIngestion);
  const [formError, setFormError] = useState("");
  const [successMessage, setSuccessMessage] = useState("");
  const mutation = useStartIngestion();
  const selectedConnection = connections.find((connection) => `${connection.id}::${connection.version}` === draft.connectionKey);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    setSuccessMessage("");
    const instruments = draft.instruments.split(",").map((value) => value.trim()).filter(Boolean);
    const mapping = parseArray(draft.mapping);
    if (!selectedConnection) return fail("请选择连接版本。", "ingestion-connection");
    if (!draft.dataset.trim()) return fail("请填写数据集标识。", "ingestion-dataset");
    if (!draft.frequency.trim()) return fail("请填写数据频率。", "ingestion-frequency");
    if (instruments.length === 0) return fail("至少填写一个 instrument ID，用逗号分隔。", "ingestion-instruments");
    if (!draft.from.trim() || !draft.to.trim()) return fail("请填写 [from, to) 时间区间，使用带时区的 ISO 8601。", "ingestion-from");
    if (!draft.mode) return fail("请选择更新模式。", "ingestion-mode");
    if (!mapping) return fail("mapping 必须是 JSON 数组。", "ingestion-mapping");
    if (!draft.timezone.trim()) return fail("请填写数据源业务时区。", "ingestion-timezone");
    if (!draft.availabilityId.trim() || !draft.availabilityVersion.trim()) return fail("请完整填写 availability policy 的 id 和 version。", "ingestion-availability-id");
    try {
      const job = await mutation.mutateAsync({
        connection_ref: { id: selectedConnection.id, version: selectedConnection.version },
        dataset: draft.dataset.trim(),
        frequency: draft.frequency.trim(),
        instrument_ids: instruments,
        range: { from: draft.from.trim(), to: draft.to.trim() },
        mode: draft.mode,
        mapping: mapping as components["schemas"]["Mapping"][],
        timezone: draft.timezone.trim(),
        availability_policy_ref: { id: draft.availabilityId.trim(), version: draft.availabilityVersion.trim() },
      });
      setDraft(initialIngestion);
      setSuccessMessage(`更新任务 ${job.id} 已受理。202 只表示已进入任务队列。`);
    } catch {
      // The normalized mutation error is rendered below.
    }
  }

  function fail(message: string, focusId: string) {
    setFormError(message);
    document.getElementById(focusId)?.focus();
  }

  return (
    <form className="stack-form ingestion-form" noValidate onSubmit={(event) => void submit(event)}>
      <div className="form-grid">
        <Field label="连接版本" id="ingestion-connection" help="仅支持已创建的 Connection 版本；上传导入将在后续切片接入。">
          <AntSelect id="ingestion-connection" value={draft.connectionKey || null} placeholder="选择连接版本" onChange={(value: string) => setDraft({ ...draft, connectionKey: value })} options={connections.map((connection) => ({ value: `${connection.id}::${connection.version}`, label: `${connection.name} · v${connection.version}` }))} aria-label="连接版本" />
          {connections.length === 0 ? <small className="field-hint"><Link to="/connections">先创建连接版本</Link></small> : null}
        </Field>
        <Field label="数据集标识" id="ingestion-dataset"><Input id="ingestion-dataset" value={draft.dataset} onChange={(event) => setDraft({ ...draft, dataset: event.target.value })} /></Field>
        <Field label="频率" id="ingestion-frequency" help="例如由 Provider 能力声明的频率；不在前端替换为市场默认值。"><Input id="ingestion-frequency" value={draft.frequency} onChange={(event) => setDraft({ ...draft, frequency: event.target.value })} /></Field>
        <Field label="instrument IDs" id="ingestion-instruments" help="逗号分隔，保持服务端 ID 原样。"><Input id="ingestion-instruments" value={draft.instruments} onChange={(event) => setDraft({ ...draft, instruments: event.target.value })} /></Field>
        <Field label="区间 from（含）" id="ingestion-from" help="带时区 ISO 8601；区间为 [from, to)。"><Input id="ingestion-from" value={draft.from} onChange={(event) => setDraft({ ...draft, from: event.target.value })} placeholder="2024-01-01T00:00:00Z" /></Field>
        <Field label="区间 to（不含）" id="ingestion-to"><Input id="ingestion-to" value={draft.to} onChange={(event) => setDraft({ ...draft, to: event.target.value })} placeholder="2024-02-01T00:00:00Z" /></Field>
        <Field label="更新模式" id="ingestion-mode"><AntSelect id="ingestion-mode" value={draft.mode || null} placeholder="选择模式" onChange={(value: IngestionDraft["mode"]) => setDraft({ ...draft, mode: value })} options={[{ value: "incremental", label: "增量" }, { value: "backfill", label: "回补" }, { value: "force", label: "强制重新获取" }]} aria-label="更新模式" /></Field>
        <Field label="业务时区" id="ingestion-timezone"><Input id="ingestion-timezone" value={draft.timezone} onChange={(event) => setDraft({ ...draft, timezone: event.target.value })} placeholder="由数据源策略指定" /></Field>
        <Field label="availability policy id" id="ingestion-availability-id"><Input id="ingestion-availability-id" value={draft.availabilityId} onChange={(event) => setDraft({ ...draft, availabilityId: event.target.value })} /></Field>
        <Field label="availability policy version" id="ingestion-availability-version"><Input id="ingestion-availability-version" value={draft.availabilityVersion} onChange={(event) => setDraft({ ...draft, availabilityVersion: event.target.value })} /></Field>
      </div>
      <Field label="字段 mapping JSON" id="ingestion-mapping" help="按 Provider 与数据集 schema 显式填写单位、缩放和目标字段；不会静默转换。">
        <Input.TextArea id="ingestion-mapping" value={draft.mapping} onChange={(event) => setDraft({ ...draft, mapping: event.target.value })} rows={4} />
      </Field>
      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {mutation.isError ? <div className="form-error"><QueryError error={mutation.error} actionLabel="关闭错误" onRetry={() => { mutation.reset(); setFormError(""); }} /></div> : null}
      {successMessage ? <Alert type="success" showIcon title={successMessage} role="status" /> : null}
      <div className="form-actions"><Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={mutation.isPending}>发起数据更新</Button><span className="muted">后台执行，离开页面后仍继续</span></div>
    </form>
  );
}

function SnapshotForm({ batches }: { batches: components["schemas"]["Batch"][] }) {
  const [draft, setDraft] = useState(initialSnapshot);
  const [formError, setFormError] = useState("");
  const [successMessage, setSuccessMessage] = useState("");
  const mutation = useStartSnapshot();
  const readyBatches = batches.filter((batch) => batch.ready);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    setSuccessMessage("");
    if (!draft.name.trim()) return fail("请填写快照名称。", "snapshot-name");
    if (draft.batchIds.length === 0) return fail("至少选择一个可发布批次。", "snapshot-batches");
    if (!draft.strictPit) return fail("请选择是否启用严格 PIT。", "snapshot-strict-pit");
    try {
      const job = await mutation.mutateAsync({ name: draft.name.trim(), batch_ids: draft.batchIds, strict_pit: draft.strictPit === "true" });
      setDraft(initialSnapshot);
      setSuccessMessage(`快照发布任务 ${job.id} 已受理。请在任务中心确认终态。`);
    } catch {
      // The normalized mutation error is rendered below.
    }
  }

  function fail(message: string, focusId: string) {
    setFormError(message);
    document.getElementById(focusId)?.focus();
  }

  return (
    <form className="stack-form" noValidate onSubmit={(event) => void submit(event)}>
      <Field label="快照名称" id="snapshot-name"><Input id="snapshot-name" value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} /></Field>
      <Field label="可发布批次" id="snapshot-batches" help="只显示 ready=true 的批次；质量问题仍随快照保留。"><AntSelect id="snapshot-batches" mode="multiple" value={draft.batchIds} placeholder={readyBatches.length ? "选择批次" : "暂无可发布批次"} onChange={(value: string[]) => setDraft({ ...draft, batchIds: value })} options={readyBatches.map((batch) => ({ value: batch.id, label: `${batch.id} · ${batch.row_count.toLocaleString("zh-CN")} 行` }))} aria-label="可发布批次" /></Field>
      <Field label="严格 PIT" id="snapshot-strict-pit" help="启用后，缺少 published_at 的记录不会进入可查询快照。"><AntSelect id="snapshot-strict-pit" value={draft.strictPit || null} placeholder="选择策略" onChange={(value: SnapshotDraft["strictPit"]) => setDraft({ ...draft, strictPit: value })} options={[{ value: "true", label: "启用严格 PIT" }, { value: "false", label: "不启用严格 PIT" }]} aria-label="严格 PIT" /></Field>
      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {mutation.isError ? <QueryError error={mutation.error} actionLabel="关闭错误" onRetry={() => { mutation.reset(); setFormError(""); }} /> : null}
      {successMessage ? <Alert type="success" showIcon title={successMessage} role="status" /> : null}
      <div className="form-actions"><Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={mutation.isPending || readyBatches.length === 0}>发布不可变快照</Button></div>
    </form>
  );
}

function Field({ label, id, help, children }: { label: string; id: string; help?: string; children: ReactNode }) {
  return <div className="field"><label htmlFor={id}>{label}</label>{children}{help ? <small id={`${id}-help`}>{help}</small> : null}</div>;
}

function parseArray(value: string): unknown[] | null {
  if (!value.trim()) return null;
  try {
    const parsed: unknown = JSON.parse(value);
    return Array.isArray(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

function QualityLabel({ issues }: { issues: components["schemas"]["Issue"][] }) {
  const hasError = issues.some((issue) => issue.severity === "error");
  const hasWarning = issues.some((issue) => issue.severity === "warning");
  if (hasError) return <span className="quality-label quality-error">有错误</span>;
  if (hasWarning) return <span className="quality-label quality-warning">有警告</span>;
  return <span className="quality-label quality-clean">无阻塞问题</span>;
}

function MetricBox({ label, value, detail, accent = false }: { label: string; value: string; detail: string; accent?: boolean }) {
  return <div className={`metric-box${accent ? " accent" : ""}`}><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>;
}

function SearchFieldAdapter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <SearchField value={value} onChange={onChange} placeholder="按名称或 ID 搜索" />;
}
