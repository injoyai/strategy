import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import type { components } from "../../api/schema";
import { useCreateUniverse, useResolveUniverse, useSnapshots, useUniverses } from "../../api/hooks";
import { EmptyState, Field, LoadMore, PageHeader, QueryError, QueryLoading, SearchField, Section, formatDateTime } from "../ui";

type UniverseDraft = {
  name: string;
  snapshotId: string;
  kind: "static" | "historical_rule" | "";
  members: string;
  ruleDataset: string;
  ruleFrequency: string;
};

type ResolveDraft = {
  universeId: string;
  asOf: string;
};

const initialDraft: UniverseDraft = {
  name: "",
  snapshotId: "",
  kind: "",
  members: "",
  ruleDataset: "",
  ruleFrequency: "",
};

export function UniversesPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const universes = useUniverses(query);
  const snapshots = useSnapshots();
  const universeItems = useMemo(() => universes.data?.pages.flatMap((page) => page.items) ?? [], [universes.data?.pages]);
  const snapshotItems = useMemo(() => snapshots.data?.pages.flatMap((page) => page.items) ?? [], [snapshots.data?.pages]);

  // The preview panel is driven by the list selection so a row can hand its
  // version straight to the resolve form without a second lookup.
  const [preview, setPreview] = useState<ResolveDraft>({ universeId: "", asOf: "" });

  function setQuery(nextQuery: string) {
    const next = new URLSearchParams(searchParams);
    if (nextQuery) next.set("q", nextQuery);
    else next.delete("q");
    setSearchParams(next);
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="研究输入 / UNIVERSE VERSIONS"
        title="标的池"
        description="每个版本绑定一个不可变快照；解析始终经过绑定该快照的视图，历史成员不会随当前表变化。"
        action={<Link className="button-link secondary-link" to="/data">查看数据中心</Link>}
      />

      <div className="metric-grid" aria-label="标的池概览">
        <MetricBox label="版本数" value={universes.isPending ? "—" : String(universeItems.length)} detail={query ? "当前搜索结果" : "当前页已加载"} />
        <MetricBox label="静态成员版本" value={universes.isPending ? "—" : String(universeItems.filter((item) => item.definition.kind === "static").length)} detail="冻结显式成员列表" />
        <MetricBox label="历史规则版本" value={universes.isPending ? "—" : String(universeItems.filter((item) => item.definition.kind === "historical_rule").length)} detail="按生效窗口解析成员" />
        <MetricBox label="快照绑定" value="必选" detail="definition_hash 不含绑定" accent />
      </div>

      <Section title="新建标的池版本" description="保存不是幂等的：每次提交都会生成一个新版本；快照与定义由服务端做存在性与语义校验。">
        {snapshots.isPending ? <QueryLoading label="正在读取可用快照" /> : snapshots.isError ? <QueryError error={snapshots.error} onRetry={() => void snapshots.refetch()} /> : <UniverseForm snapshots={snapshotItems} />}
      </Section>

      <Section title="成员预览" description="只读预览：快照来自版本的绑定，请求只提供决策时点 as_of。">
        <ResolveForm universes={universeItems} draft={preview} onChange={setPreview} />
      </Section>

      <Section title="版本列表" description="definition 是服务端规范化（排序去重）后的形式；definition_hash 只覆盖定义本身。">
        <div className="table-toolbar">
          <SearchFieldAdapter value={query} onChange={setQuery} />
          <span className="result-count">{universeItems.length} 条已加载</span>
        </div>
        {universes.isPending ? <QueryLoading label="正在读取标的池版本" /> : universes.isError ? <QueryError error={universes.error} onRetry={() => void universes.refetch()} /> : universeItems.length === 0 ? (
          <EmptyState title={query ? "没有匹配的标的池" : "还没有标的池版本"} description={query ? "清除搜索条件后查看全部版本。" : "保存一个静态成员列表或历史规则后，版本会出现在这里。"} />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <caption className="sr-only">标的池版本列表</caption>
                <thead><tr><th scope="col">版本</th><th scope="col">定义</th><th scope="col">快照</th><th scope="col">definition_hash</th><th scope="col">创建时间</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  {universeItems.map((universe) => {
                    const described = describeDefinition(universe.definition);
                    return (
                      <tr key={universe.id}>
                        <th scope="row"><span>{universe.name}</span><small className="mono block">{universe.id}</small></th>
                        <td><span className="soft-label">{described.kind}</span><small className="block">{described.detail}</small></td>
                        <td className="mono">{universe.snapshot_id}</td>
                        <td className="mono truncate-cell">{universe.definition_hash}</td>
                        <td>{formatDateTime(universe.created_at)}</td>
                        <td><Button type="link" onClick={() => setPreview({ universeId: universe.id, asOf: preview.universeId === universe.id ? preview.asOf : "" })}>预览成员</Button></td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <LoadMore hasMore={Boolean(universes.hasNextPage)} loading={universes.isFetchingNextPage} onClick={() => void universes.fetchNextPage()} />
          </>
        )}
      </Section>

      <p className="page-footnote">成员是状态数据：缩短成员的修正不会回改历史样本，拉长成员会形成合法的“成员 → 知识缺口 → 修正”序列。</p>
      <Link className="quiet-link" to="/factors">查看因子库 →</Link>
    </div>
  );
}

function UniverseForm({ snapshots }: { snapshots: components["schemas"]["Snapshot"][] }) {
  const [draft, setDraft] = useState(initialDraft);
  const [formError, setFormError] = useState("");
  const [created, setCreated] = useState<components["schemas"]["Universe"] | null>(null);
  const mutation = useCreateUniverse();

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    setCreated(null);
    if (!draft.name.trim()) return fail("请填写标的池名称。", "universe-name");
    if (!draft.snapshotId) return fail("请选择绑定的快照。", "universe-snapshot");
    if (!draft.kind) return fail("请选择定义类型。", "universe-kind");
    let definition: components["schemas"]["UniverseDefinition"];
    if (draft.kind === "static") {
      const members = draft.members.split(/[\s,]+/).map((value) => value.trim()).filter(Boolean);
      if (members.length === 0) return fail("至少填写一个成员 ID，用逗号或换行分隔。", "universe-members");
      definition = { kind: "static", members };
    } else {
      if (!draft.ruleDataset.trim()) return fail("请填写历史规则的数据集。", "universe-rule-dataset");
      if (!draft.ruleFrequency.trim()) return fail("请填写历史规则的频率。", "universe-rule-frequency");
      definition = { kind: "historical_rule", rule: { dataset: draft.ruleDataset.trim(), frequency: draft.ruleFrequency.trim() } };
    }
    try {
      const universe = await mutation.mutateAsync({ name: draft.name.trim(), snapshot_id: draft.snapshotId, definition });
      setDraft(initialDraft);
      setCreated(universe);
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
      <div className="form-grid">
        <Field label="名称" id="universe-name"><Input id="universe-name" value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} /></Field>
        <Field label="绑定快照" id="universe-snapshot" help="解析只用该快照的视图，不会读取当前表。">
          <AntSelect id="universe-snapshot" value={draft.snapshotId || null} placeholder={snapshots.length ? "选择快照" : "暂无可绑定快照"} onChange={(value: string) => setDraft({ ...draft, snapshotId: value })} options={snapshots.map((snapshot) => ({ value: snapshot.id, label: `${snapshot.name} · ${snapshot.strict_pit ? "严格 PIT" : "非严格 PIT"}` }))} aria-label="绑定快照" />
          {snapshots.length === 0 ? <small className="field-hint"><Link to="/data">先在数据中心发布快照</Link></small> : null}
        </Field>
        <Field label="定义类型" id="universe-kind">
          <AntSelect id="universe-kind" value={draft.kind || null} placeholder="选择类型" onChange={(value: UniverseDraft["kind"]) => setDraft({ ...draft, kind: value })} options={[{ value: "static", label: "静态成员" }, { value: "historical_rule", label: "历史规则" }]} aria-label="定义类型" />
        </Field>
        {draft.kind === "historical_rule" ? (
          <>
            <Field label="规则数据集" id="universe-rule-dataset" help="按该数据集在快照中的生效窗口解析成员。"><Input id="universe-rule-dataset" value={draft.ruleDataset} onChange={(event) => setDraft({ ...draft, ruleDataset: event.target.value })} /></Field>
            <Field label="规则频率" id="universe-rule-frequency"><Input id="universe-rule-frequency" value={draft.ruleFrequency} onChange={(event) => setDraft({ ...draft, ruleFrequency: event.target.value })} /></Field>
          </>
        ) : null}
      </div>
      {draft.kind === "static" ? (
        <Field label="静态成员 ID" id="universe-members" help="逗号或换行分隔；服务端会排序去重后再计算 definition_hash。">
          <Input.TextArea id="universe-members" value={draft.members} onChange={(event) => setDraft({ ...draft, members: event.target.value })} rows={4} />
        </Field>
      ) : null}
      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {mutation.isError ? <div className="form-error"><QueryError error={mutation.error} actionLabel="关闭错误" onRetry={() => { mutation.reset(); setFormError(""); }} /></div> : null}
      {created ? <Alert type="success" showIcon title={`版本 ${created.id} 已创建。`} description={<span className="mono">definition_hash {created.definition_hash}</span>} role="status" /> : null}
      <div className="form-actions"><Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={mutation.isPending}>保存为新版本</Button><span className="muted">每次保存都会生成新版本</span></div>
    </form>
  );
}

function ResolveForm({ universes, draft, onChange }: {
  universes: components["schemas"]["Universe"][];
  draft: ResolveDraft;
  onChange: (next: ResolveDraft) => void;
}) {
  const mutation = useResolveUniverse();
  // Kept so a failed preview can be retried with exactly the request that
  // failed instead of re-reading the form.
  const [lastRequest, setLastRequest] = useState<{ id: string; asOf: string } | null>(null);

  async function resolve(request: { id: string; asOf: string }) {
    setLastRequest(request);
    try {
      await mutation.mutateAsync({ id: request.id, body: { as_of: request.asOf } });
    } catch {
      // The normalized mutation error is rendered below.
    }
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!draft.universeId || !draft.asOf.trim()) return;
    void resolve({ id: draft.universeId, asOf: draft.asOf.trim() });
  }

  return (
    <form className="stack-form" noValidate onSubmit={submit}>
      <div className="form-grid">
        <Field label="标的池版本" id="resolve-universe">
          <AntSelect id="resolve-universe" value={draft.universeId || null} placeholder={universes.length ? "选择版本" : "暂无版本"} onChange={(value: string) => onChange({ ...draft, universeId: value })} options={universes.map((universe) => ({ value: universe.id, label: `${universe.name} · ${universe.id}` }))} aria-label="标的池版本" />
        </Field>
        <Field label="决策时点 as_of" id="resolve-as-of" help="带时区 ISO 8601；成员按该时点的生效窗口解析。"><Input id="resolve-as-of" value={draft.asOf} onChange={(event) => onChange({ ...draft, asOf: event.target.value })} placeholder="2026-01-15T00:00:00Z" /></Field>
      </div>
      {mutation.isError ? <QueryError error={mutation.error} onRetry={() => { if (lastRequest) void resolve(lastRequest); }} /> : null}
      {mutation.data ? (
        <div className="resolve-result" role="status">
          <p><strong>{mutation.data.count}</strong> 个成员 · as_of <span className="mono">{mutation.data.as_of}</span></p>
          {mutation.data.count === 0 ? <p className="muted">空成员是合法结果，不是错误。</p> : <ul className="chip-list">{mutation.data.instrument_ids.map((id) => <li key={id} className="mono">{id}</li>)}</ul>}
        </div>
      ) : null}
      <div className="form-actions"><Button type="primary" htmlType="submit" loading={mutation.isPending} disabled={mutation.isPending || !draft.universeId || !draft.asOf.trim()}>预览成员</Button></div>
    </form>
  );
}

function describeDefinition(definition: components["schemas"]["UniverseDefinition"]): { kind: string; detail: string } {
  if (definition.kind === "static") {
    return { kind: "静态成员", detail: `${definition.members.length} 个成员` };
  }
  return { kind: "历史规则", detail: `${definition.rule.dataset} · ${definition.rule.frequency}` };
}

function MetricBox({ label, value, detail, accent = false }: { label: string; value: string; detail: string; accent?: boolean }) {
  return <div className={`metric-box${accent ? " accent" : ""}`}><span>{label}</span><strong>{value}</strong><small>{detail}</small></div>;
}

function SearchFieldAdapter({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <SearchField value={value} onChange={onChange} placeholder="按名称或 ID 搜索" />;
}
