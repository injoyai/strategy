import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useCreateScreener, useScreeners, useScreenRuns, useScreener } from "../../api/hooks";
import {
  buildScreenerCreate,
  childPath,
  draftFromScreener,
  emptyScreenerDraft,
  emptyValue,
  newBinding,
  newNode,
  type BindingDraft,
  type CompareOperator,
  type ConditionKind,
  type NodeDraft,
  type ParamKind,
  type ScreenerDraft,
  type ValueDraft,
  type ValueKind,
} from "../screener-draft";
import { EmptyState, Field, LoadMore, PageHeader, QueryError, QueryLoading, SearchField, Section, formatDateTime } from "../ui";

/**
 * The screening surface's plan half: the immutable screener revisions and the
 * editor that produces them. Saving never runs anything — a run binds a
 * snapshot, a pool and a decision time, so it lives on the run page.
 */
export function ScreenersPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const screeners = useScreeners(query);
  const items = screeners.data?.pages.flatMap((page) => page.items) ?? [];

  function setQuery(nextQuery: string) {
    const next = new URLSearchParams(searchParams);
    if (nextQuery) next.set("q", nextQuery);
    else next.delete("q");
    setSearchParams(next);
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="选股 / SCREENER VERSIONS"
        title="选股方案"
        description="方案是注册的不可变修订：输入绑定、条件树、排序/评分与数量。修改会创建新版本，旧版本与它跑过的结果都保持不变。"
        action={<Link className="button-link primary-link" to="/screeners/new">新建方案</Link>}
      />

      <Section title="方案目录" description="服务端分页；每个修订是独立一行，修改后的新版本不会覆盖旧记录。">
        <div className="table-toolbar">
          <SearchField value={query} onChange={setQuery} placeholder="按方案名称或 ID 搜索" />
          <span className="result-count">{items.length} 条已加载</span>
        </div>
        {screeners.isPending ? (
          <QueryLoading label="正在读取选股方案" />
        ) : screeners.isError ? (
          <QueryError error={screeners.error} onRetry={() => void screeners.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState
            title={query ? "没有匹配方案" : "还没有选股方案"}
            description={query ? "清除搜索条件后查看全部方案。" : "新建一个方案，定义输入、条件与排名规则。"}
            action={<Link className="button-link primary-link" to="/screeners/new">新建方案</Link>}
          />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table">
                <caption className="sr-only">选股方案版本</caption>
                <thead>
                  <tr>
                    <th scope="col">方案</th>
                    <th scope="col">修订</th>
                    <th scope="col">规则 schema</th>
                    <th scope="col">创建时间</th>
                    <th scope="col">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {items.map((screener) => (
                    <tr key={`${screener.id}@${screener.version}`}>
                      <th scope="row">
                        <span>{screener.name}</span>
                        <small className="mono block">{screener.id}@{screener.version}</small>
                      </th>
                      <td className="mono">{screener.version}</td>
                      <td className="mono">{screener.rule_schema_version}</td>
                      <td>{formatDateTime(screener.created_at)}</td>
                      <td>
                        <Link className="table-link" to={screenerHref(screener.id, screener.version)}>查看与运行</Link>
                        <Link className="table-link" to={`/screeners/new?from=${encodeURIComponent(screener.id)}&fromVersion=${encodeURIComponent(screener.version)}`}>基于此版本新建</Link>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <LoadMore hasMore={Boolean(screeners.hasNextPage)} loading={screeners.isFetchingNextPage} onClick={() => void screeners.fetchNextPage()} />
          </>
        )}
      </Section>

      <Section title="最近运行" description="运行结果与它的冻结配置一起持久化；未发布前结果不可读。">
        <ScreenerRunList />
      </Section>
    </div>
  );
}

/** The run listing is a separate surface: /screen-runs carries no state of its
 * own, so the job state filter is what tells a failure from a success. */
function ScreenerRunList() {
  const [state, setState] = useState("");
  const runs = useScreenRuns("", state);
  const items = useMemo(() => runs.data?.pages.flatMap((page) => page.items) ?? [], [runs.data?.pages]);
  return (
    <div className="stack-form">
      <div className="form-grid">
        <Field label="任务状态" id="run-state-filter" help="状态来自运行对应的 Job；运行本身不持有状态。">
          <AntSelect
            id="run-state-filter"
            value={state || null}
            placeholder="全部状态"
            allowClear
            onChange={(value: string | undefined) => setState(value ?? "")}
            options={[
              { value: "queued", label: "等待执行" },
              { value: "running", label: "执行中" },
              { value: "succeeded", label: "已完成" },
              { value: "failed", label: "失败" },
              { value: "cancelled", label: "已取消" },
            ]}
            aria-label="任务状态"
          />
        </Field>
      </div>
      {runs.isPending ? (
        <QueryLoading label="正在读取选股运行" />
      ) : runs.isError ? (
        <QueryError error={runs.error} onRetry={() => void runs.refetch()} />
      ) : items.length === 0 ? (
        <EmptyState title="没有运行记录" description="在方案详情页提交运行后，这里会显示它的冻结配置与摘要。" />
      ) : (
        <>
          <div className="table-scroll">
            <table className="data-table">
              <caption className="sr-only">选股运行</caption>
              <thead>
                <tr>
                  <th scope="col">运行</th>
                  <th scope="col">方案</th>
                  <th scope="col">决策时点</th>
                  <th scope="col">入选</th>
                  <th scope="col">操作</th>
                </tr>
              </thead>
              <tbody>
                {items.map((run) => (
                  <tr key={run.id}>
                    <th scope="row" className="mono">{run.id}</th>
                    <td className="mono">{run.config.screener_ref.id}@{run.config.screener_ref.version}</td>
                    <td className="mono">{run.config.as_of}</td>
                    <td>{run.summary === null ? <span className="soft-label">未发布</span> : run.summary.selected}</td>
                    <td><Link className="table-link" to={`/screen-runs/${encodeURIComponent(run.id)}`}>查看运行</Link></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <LoadMore hasMore={Boolean(runs.hasNextPage)} loading={runs.isFetchingNextPage} onClick={() => void runs.fetchNextPage()} />
        </>
      )}
    </div>
  );
}

export function screenerHref(id: string, version: string): string {
  return `/screeners/${encodeURIComponent(id)}?version=${encodeURIComponent(version)}`;
}

/**
 * The screener editor. Creating from an existing revision pre-fills the draft
 * and carries parent_id, so the API records a new revision of that identity
 * instead of an unrelated screener.
 */
export function ScreenerEditorPage() {
  const [searchParams] = useSearchParams();
  const fromId = searchParams.get("from") ?? "";
  const fromVersion = searchParams.get("fromVersion") ?? "";
  const base = useScreener(fromId && fromVersion ? { id: fromId, version: fromVersion } : undefined);
  // A plain /screeners/new starts empty; "基于此版本新建" seeds from the loaded
  // revision. Seeding happens once, so later edits are never overwritten.
  const [draft, setDraft] = useState<ScreenerDraft | null>(fromId ? null : emptyScreenerDraft());
  useEffect(() => {
    if (base.data) {
      setDraft(draftFromScreener(base.data));
    }
  }, [base.data]);

  if (fromId && base.isPending) {
    return <QueryLoading label="正在读取基础版本" />;
  }
  if (fromId && base.isError) {
    return <QueryError error={base.error} onRetry={() => void base.refetch()} />;
  }
  if (!draft) {
    return <QueryLoading label="正在准备草稿" />;
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="选股 / SCREENER EDITOR"
        title={fromId ? "基于此版本新建" : "新建选股方案"}
        description="保存只记录不可变修订，不会运行选股。运行需要额外绑定快照、标的池与决策时点。"
        action={<Link className="button-link secondary-link" to="/screeners">返回方案目录</Link>}
      />
      <ScreenerForm draft={draft} onChange={setDraft} />
    </div>
  );
}

function ScreenerForm({ draft, onChange }: { draft: ScreenerDraft; onChange: (next: ScreenerDraft) => void }) {
  const create = useCreateScreener();
  const navigate = useNavigate();
  const [formError, setFormError] = useState("");
  const bindingIds = draft.bindings.map((binding) => binding.bindingId).filter((id) => id.trim());

  function submit() {
    setFormError("");
    const built = buildScreenerCreate(draft);
    if (!built.ok) {
      setFormError(built.message);
      document.getElementById(built.focusId)?.focus();
      return;
    }
    create.mutate(built.value, {
      onSuccess: (screener) => navigate(screenerHref(screener.id, screener.version)),
    });
  }

  return (
    <div className="stack-form">
      <Section title="基本信息" description="name 是方案身份的标签；parent_id 记录它从哪个修订派生。">
        <div className="form-grid">
          <Field label="方案名称" id="screener-name">
            <Input id="screener-name" value={draft.name} onChange={(event) => onChange({ ...draft, name: event.target.value })} placeholder="例如：质量动量池" />
          </Field>
          <Field label="描述" id="screener-description" help="可留空；描述不参与规则哈希。">
            <Input id="screener-description" value={draft.description} onChange={(event) => onChange({ ...draft, description: event.target.value })} />
          </Field>
          <Field label="上级修订 parent_id" id="screener-parent" help="留空表示新建一个方案身份；非空表示给该身份追加修订。">
            <Input id="screener-parent" value={draft.parentId} onChange={(event) => onChange({ ...draft, parentId: event.target.value })} placeholder="scr_..." />
          </Field>
        </div>
      </Section>

      <Section title="输入绑定" description="每个绑定有唯一的 binding_id；字段绑定读数据集目录，因子绑定引用注册的因子版本与参数。">
        <BindingEditor draft={draft} onChange={onChange} />
      </Section>

      <Section title="条件树" description="三值逻辑：AND 以 false 为主、OR 以 true 为主，NOT unknown 仍是 unknown。只有根节点为 true 的标的进入排名。">
        <NodeEditor draft={draft} node={draft.root} depth={0} onChange={onChange} />
        <p className="muted">节点在同一方案内需要唯一 node_id；解释会按 node_id 定位到具体条件。</p>
      </Section>

      <Section title="排名与数量" description="排序与加权评分互斥；instrument_id 升序始终作为稳定决胜键。">
        <RankingEditor draft={draft} bindingIds={bindingIds} onChange={onChange} />
      </Section>

      <Section title="展示列" description="展示列只影响结果表与解释的附加字段，不参与入选判断；缺失按原因单独标注。">
        <div className="checkbox-row">
          {bindingIds.length === 0 ? <span className="muted">先声明输入绑定。</span> : bindingIds.map((id) => (
            <label key={id} className="checkbox-item">
              <input
                type="checkbox"
                checked={draft.displayColumns.includes(id)}
                onChange={(event) =>
                  onChange({
                    ...draft,
                    displayColumns: event.target.checked
                      ? [...draft.displayColumns, id]
                      : draft.displayColumns.filter((column) => column !== id),
                  })
                }
              />
              <span className="mono">{id}</span>
            </label>
          ))}
        </div>
      </Section>

      {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
      {create.isError ? <QueryError error={create.error} onRetry={submit} actionLabel="重新保存" /> : null}
      <div className="form-actions">
        <Button type="primary" htmlType="button" loading={create.isPending} disabled={create.isPending} onClick={submit}>
          保存为新版本
        </Button>
        <span className="muted">保存不会运行选股，也不修改任何既有版本</span>
      </div>
    </div>
  );
}

function BindingEditor({ draft, onChange }: { draft: ScreenerDraft; onChange: (next: ScreenerDraft) => void }) {
  function update(index: number, next: Partial<BindingDraft>) {
    const bindings = draft.bindings.map((binding, position) => (position === index ? { ...binding, ...next } : binding));
    onChange({ ...draft, bindings });
  }

  return (
    <div className="stack-form">
      {draft.bindings.map((binding, index) => (
        <div className="repeat-row" key={binding.key}>
          <div className="form-grid">
            <Field label={`binding_id #${index + 1}`} id={`binding-id-${binding.key}`} help="条件、排序与展示列都用它引用该输入。">
              <Input id={`binding-id-${binding.key}`} value={binding.bindingId} onChange={(event) => update(index, { bindingId: event.target.value })} placeholder="px" className="mono" />
            </Field>
            <Field label="绑定类型" id={`binding-kind-${binding.key}`}>
              <AntSelect
                id={`binding-kind-${binding.key}`}
                value={binding.kind}
                onChange={(value: BindingDraft["kind"]) => update(index, { kind: value })}
                options={[
                  { value: "field", label: "字段（数据集目录）" },
                  { value: "factor", label: "因子（注册版本）" },
                ]}
                aria-label="绑定类型"
              />
            </Field>
            {binding.kind === "field" ? (
              <>
                <Field label="数据集" id={`binding-dataset-${binding.key}`}>
                  <Input id={`binding-dataset-${binding.key}`} value={binding.dataset} onChange={(event) => update(index, { dataset: event.target.value })} placeholder="bar" className="mono" />
                </Field>
                <Field label="字段" id={`binding-field-${binding.key}`} help="字段类型与单位由数据集目录提供，预检时比对。">
                  <Input id={`binding-field-${binding.key}`} value={binding.field} onChange={(event) => update(index, { field: event.target.value })} placeholder="close" className="mono" />
                </Field>
              </>
            ) : (
              <>
                <Field label="因子 id" id={`binding-factor-${binding.key}`}>
                  <Input id={`binding-factor-${binding.key}`} value={binding.factorId} onChange={(event) => update(index, { factorId: event.target.value })} placeholder="momentum" className="mono" />
                </Field>
                <Field label="因子 version" id={`binding-factor-version-${binding.key}`}>
                  <Input id={`binding-factor-version-${binding.key}`} value={binding.factorVersion} onChange={(event) => update(index, { factorVersion: event.target.value })} placeholder="1.0.0" className="mono" />
                </Field>
              </>
            )}
          </div>
          {binding.kind === "factor" ? (
            <div className="repeat-row">
              <p className="muted">参数（按因子 schema 填写，留空即使用声明默认值）</p>
              {binding.params.map((param, paramIndex) => (
                <div className="inline-fields" key={`${binding.key}-param-${paramIndex}`}>
                  <Input
                    id={`param-name-${binding.key}`}
                    value={param.name}
                    placeholder="n"
                    className="mono"
                    aria-label="参数名"
                    onChange={(event) => update(index, { params: replaceParam(binding.params, paramIndex, { name: event.target.value }) })}
                  />
                  <AntSelect
                    value={param.type}
                    onChange={(value: ParamKind) => update(index, { params: replaceParam(binding.params, paramIndex, { type: value }) })}
                    options={[
                      { value: "integer", label: "整数" },
                      { value: "number", label: "数字" },
                      { value: "decimal", label: "小数字符串" },
                      { value: "string", label: "文本" },
                      { value: "boolean", label: "布尔" },
                    ]}
                    aria-label="参数类型"
                  />
                  <Input
                    id={`param-value-${binding.key}`}
                    value={param.text}
                    placeholder="20"
                    aria-label="参数值"
                    onChange={(event) => update(index, { params: replaceParam(binding.params, paramIndex, { text: event.target.value }) })}
                  />
                  <Button type="text" onClick={() => update(index, { params: binding.params.filter((_, position) => position !== paramIndex) })} aria-label="删除参数">删除</Button>
                </div>
              ))}
              <Button type="dashed" onClick={() => update(index, { params: [...binding.params, { name: "", type: "number", text: "" }] })}>添加参数</Button>
            </div>
          ) : null}
          <div className="form-actions">
            <Button type="text" danger onClick={() => onChange({ ...draft, bindings: draft.bindings.filter((_, position) => position !== index) })} disabled={draft.bindings.length <= 1}>
              删除绑定
            </Button>
          </div>
        </div>
      ))}
      <Button id="binding-add" type="dashed" onClick={() => onChange({ ...draft, bindings: [...draft.bindings, newBinding(`col${draft.bindings.length + 1}`)] })}>
        添加绑定
      </Button>
    </div>
  );
}

function replaceParam(params: BindingDraft["params"], index: number, next: Partial<BindingDraft["params"][number]>) {
  return params.map((param, position) => (position === index ? { ...param, ...next } : param));
}

/** Conditions are edited with buttons and selects, never by dragging: the
 * keyboard path is the primary path (AND/OR/NOT added per node). */
function NodeEditor({ draft, node, depth, onChange }: { draft: ScreenerDraft; node: NodeDraft; depth: number; onChange: (next: ScreenerDraft) => void }) {
  const bindingIds = draft.bindings.map((binding) => binding.bindingId).filter((id) => id.trim());

  function updateNode(next: NodeDraft) {
    onChange({ ...draft, root: replaceNode(draft.root, node.key, next) });
  }

  function addChild(kind: ConditionKind) {
    const child = newNode(kind, childPath(node), node.children.length);
    updateNode({ ...node, children: [...node.children, child] });
  }

  return (
    <div className={`node-editor depth-${Math.min(depth, 3)}`}>
      <div className="inline-fields">
        <Input id={`node-${node.key}`} value={node.nodeId} onChange={(event) => updateNode({ ...node, nodeId: event.target.value })} className="mono" aria-label="node_id" placeholder="node_id" />
        <AntSelect
          value={node.kind}
          onChange={(value: ConditionKind) => updateNode({ ...newNode(value, childPath(node), 0), key: node.key, nodeId: node.nodeId })}
          options={CONDITION_KINDS}
          aria-label="节点类型"
        />
        {depth > 0 ? <Button type="text" danger onClick={() => onChange({ ...draft, root: removeNode(draft.root, node.key) })} aria-label="删除节点">删除节点</Button> : null}
      </div>

      {node.kind === "all" || node.kind === "any" ? (
        <div className="node-children">
          {node.children.map((child, index) => (
            <div key={child.key}>
              <NodeEditor draft={draft} node={child} depth={depth + 1} onChange={onChange} />
              <div className="form-actions">
                <Button type="text" onClick={() => updateNode({ ...node, children: moveNode(node.children, index, -1) })} disabled={index === 0} aria-label="上移">上移</Button>
                <Button type="text" onClick={() => updateNode({ ...node, children: moveNode(node.children, index, 1) })} disabled={index === node.children.length - 1} aria-label="下移">下移</Button>
              </div>
            </div>
          ))}
          <div className="form-actions">
            <Button type="dashed" onClick={() => addChild("compare")}>添加比较条件</Button>
            <Button type="dashed" onClick={() => addChild("range")}>添加区间条件</Button>
            <Button type="dashed" onClick={() => addChild("set")}>添加集合条件</Button>
            <Button type="dashed" onClick={() => addChild("missing")}>添加缺失条件</Button>
            <Button type="dashed" onClick={() => addChild("all")}>添加 AND 组</Button>
            <Button type="dashed" onClick={() => addChild("any")}>添加 OR 组</Button>
            <Button type="dashed" onClick={() => addChild("not")}>添加 NOT</Button>
          </div>
        </div>
      ) : null}

      {node.kind === "not" ? (
        <div className="node-children">
          {node.child ? <NodeEditor draft={draft} node={node.child} depth={depth + 1} onChange={onChange} /> : (
            <Button type="dashed" onClick={() => updateNode({ ...node, child: newNode("compare", childPath(node), 0) })}>添加被否定的子节点</Button>
          )}
        </div>
      ) : null}

      {node.kind === "compare" || node.kind === "range" || node.kind === "set" || node.kind === "missing" ? (
        <div className="form-grid">
          <Field label="引用输入" id={`node-binding-${node.key}`}>
            <AntSelect
              value={node.bindingId || null}
              placeholder={bindingIds.length ? "选择 binding_id" : "先声明输入"}
              onChange={(value: string) => updateNode({ ...node, bindingId: value })}
              options={bindingIds.map((id) => ({ value: id, label: id }))}
              aria-label="引用输入"
            />
          </Field>
          {node.kind === "compare" ? (
            <>
              <Field label="操作符" id={`node-operator-${node.key}`}>
                <AntSelect value={node.operator} onChange={(value: CompareOperator) => updateNode({ ...node, operator: value })} options={COMPARE_OPERATORS} aria-label="操作符" />
              </Field>
              <ValueEditor label="比较值" idPrefix={`node-value-${node.key}`} value={node.value} onChange={(value) => updateNode({ ...node, value })} />
            </>
          ) : null}
          {node.kind === "range" ? (
            <>
              <Field label="含下界" id={`node-lower-present-${node.key}`} help="至少保留一个边界；开区间只发一个 null 边界。">
                <AntSelect
                  value={node.hasLower ? "yes" : "no"}
                  onChange={(value: string) => updateNode({ ...node, hasLower: value === "yes" })}
                  options={[{ value: "yes", label: "有下界" }, { value: "no", label: "无下界" }]}
                  aria-label="含下界"
                />
              </Field>
              {node.hasLower ? (
                <>
                  <ValueEditor label="下界" idPrefix={`node-lower-${node.key}`} value={node.lower} onChange={(value) => updateNode({ ...node, lower: value })} />
                  <Field label="下界包含" id={`node-lower-inclusive-${node.key}`}>
                    <AntSelect
                      value={node.lowerInclusive ? "closed" : "open"}
                      onChange={(value: string) => updateNode({ ...node, lowerInclusive: value === "closed" })}
                      options={[{ value: "closed", label: "包含（≥）" }, { value: "open", label: "不包含（>）" }]}
                      aria-label="下界包含"
                    />
                  </Field>
                </>
              ) : null}
              <Field label="含上界" id={`node-upper-present-${node.key}`}>
                <AntSelect
                  value={node.hasUpper ? "yes" : "no"}
                  onChange={(value: string) => updateNode({ ...node, hasUpper: value === "yes" })}
                  options={[{ value: "yes", label: "有上界" }, { value: "no", label: "无上界" }]}
                  aria-label="含上界"
                />
              </Field>
              {node.hasUpper ? (
                <>
                  <ValueEditor label="上界" idPrefix={`node-upper-${node.key}`} value={node.upper} onChange={(value) => updateNode({ ...node, upper: value })} />
                  <Field label="上界包含" id={`node-upper-inclusive-${node.key}`}>
                    <AntSelect
                      value={node.upperInclusive ? "closed" : "open"}
                      onChange={(value: string) => updateNode({ ...node, upperInclusive: value === "closed" })}
                      options={[{ value: "open", label: "不包含（<）" }, { value: "closed", label: "包含（≤）" }]}
                      aria-label="上界包含"
                    />
                  </Field>
                </>
              ) : null}
            </>
          ) : null}
          {node.kind === "set" ? (
            <>
              <Field label="集合语义" id={`node-set-mode-${node.key}`}>
                <AntSelect
                  value={node.setNotIn ? "not_in" : "in"}
                  onChange={(value: string) => updateNode({ ...node, setNotIn: value === "not_in" })}
                  options={[{ value: "in", label: "属于 in" }, { value: "not_in", label: "不属于 not_in" }]}
                  aria-label="集合语义"
                />
              </Field>
              <div className="repeat-row">
                {node.setValues.map((member, index) => (
                  <div className="inline-fields" key={`${node.key}-set-${index}`}>
                    <ValueEditor
                      label={`集合成员 #${index + 1}`}
                      idPrefix={`node-set-${node.key}-${index}`}
                      value={member}
                      onChange={(value) => updateNode({ ...node, setValues: node.setValues.map((item, position) => (position === index ? value : item)) })}
                    />
                    <Button type="text" danger onClick={() => updateNode({ ...node, setValues: node.setValues.filter((_, position) => position !== index) })} disabled={node.setValues.length <= 1} aria-label="删除集合成员">删除成员</Button>
                  </div>
                ))}
                <Button type="dashed" onClick={() => updateNode({ ...node, setValues: [...node.setValues, emptyValue()] })}>添加集合成员</Button>
              </div>
            </>
          ) : null}
          {node.kind === "missing" ? (
            <Field label="缺失语义" id={`node-missing-${node.key}`} help="unknown 不会因为 NOT 变成 true；缺失检测直接判定是否可读。">
              <AntSelect
                value={node.isMissing ? "missing" : "present"}
                onChange={(value: string) => updateNode({ ...node, isMissing: value === "missing" })}
                options={[{ value: "missing", label: "缺失 is_missing" }, { value: "present", label: "存在 is_present" }]}
                aria-label="缺失语义"
              />
            </Field>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function ValueEditor({ label, idPrefix, value, onChange }: { label: string; idPrefix: string; value: ValueDraft; onChange: (next: ValueDraft) => void }) {
  return (
    <Field label={label} id={idPrefix}>
      <div className="inline-fields">
        <AntSelect
          value={value.kind}
          onChange={(kind: ValueKind) => onChange({ ...value, kind })}
          options={VALUE_KINDS}
          aria-label={`${label}类型`}
        />
        <Input id={idPrefix} value={value.text} onChange={(event) => onChange({ ...value, text: event.target.value })} className="mono" aria-label={label} placeholder={value.kind === "boolean" ? "true / false" : "10.5"} />
      </div>
    </Field>
  );
}

function RankingEditor({ draft, bindingIds, onChange }: { draft: ScreenerDraft; bindingIds: string[]; onChange: (next: ScreenerDraft) => void }) {
  return (
    <div className="stack-form">
      <Field label="排名模式" id="ranking-mode" help="排序与评分互斥：排序按字段方向，评分按权重加总百分位。">
        <AntSelect
          id="ranking-mode"
          value={draft.rankingMode}
          onChange={(value: ScreenerDraft["rankingMode"]) => onChange({ ...draft, rankingMode: value })}
          options={[{ value: "sort", label: "多字段排序" }, { value: "score", label: "加权评分" }]}
          aria-label="排名模式"
        />
      </Field>

      {draft.rankingMode === "sort" ? (
        <div className="repeat-row">
          {draft.sortFields.map((field, index) => (
            <div className="inline-fields" key={`sort-${index}`}>
              <AntSelect
                id={`ranking-sort-${index}`}
                value={field.bindingId || null}
                placeholder={bindingIds.length ? "排序字段" : "先声明输入"}
                onChange={(value: string) => onChange({ ...draft, sortFields: draft.sortFields.map((item, position) => (position === index ? { ...item, bindingId: value } : item)) })}
                options={bindingIds.map((id) => ({ value: id, label: id }))}
                aria-label="排序字段"
              />
              <AntSelect
                value={field.direction}
                onChange={(value: "asc" | "desc") => onChange({ ...draft, sortFields: draft.sortFields.map((item, position) => (position === index ? { ...item, direction: value } : item)) })}
                options={[{ value: "desc", label: "降序" }, { value: "asc", label: "升序" }]}
                aria-label="排序方向"
              />
              <Button type="text" danger onClick={() => onChange({ ...draft, sortFields: draft.sortFields.filter((_, position) => position !== index) })} disabled={draft.sortFields.length <= 1} aria-label="删除排序字段">删除</Button>
            </div>
          ))}
          <Button id="ranking-add-sort" type="dashed" onClick={() => onChange({ ...draft, sortFields: [...draft.sortFields, { bindingId: bindingIds[0] ?? "", direction: "desc" }] })}>添加排序字段</Button>
        </div>
      ) : (
        <div className="repeat-row">
          {draft.scoreComponents.map((component, index) => (
            <div className="inline-fields" key={`score-${index}`}>
              <AntSelect
                id={`ranking-score-binding-${index}`}
                value={component.bindingId || null}
                placeholder={bindingIds.length ? "评分分量" : "先声明输入"}
                onChange={(value: string) => onChange({ ...draft, scoreComponents: draft.scoreComponents.map((item, position) => (position === index ? { ...item, bindingId: value } : item)) })}
                options={bindingIds.map((id) => ({ value: id, label: id }))}
                aria-label="评分分量"
              />
              <Input
                id={`ranking-score-weight-${index}`}
                value={component.weight}
                className="mono"
                aria-label="权重"
                onChange={(event) => onChange({ ...draft, scoreComponents: draft.scoreComponents.map((item, position) => (position === index ? { ...item, weight: event.target.value } : item)) })}
              />
              <AntSelect
                value={component.direction}
                onChange={(value: "larger_is_better" | "smaller_is_better") => onChange({ ...draft, scoreComponents: draft.scoreComponents.map((item, position) => (position === index ? { ...item, direction: value } : item)) })}
                options={[{ value: "larger_is_better", label: "越大越好" }, { value: "smaller_is_better", label: "越小越好" }]}
                aria-label="评分方向"
              />
              <Button type="text" danger onClick={() => onChange({ ...draft, scoreComponents: draft.scoreComponents.filter((_, position) => position !== index) })} disabled={draft.scoreComponents.length <= 1} aria-label="删除评分分量">删除</Button>
            </div>
          ))}
          <Button id="ranking-add-score" type="dashed" onClick={() => onChange({ ...draft, scoreComponents: [...draft.scoreComponents, { bindingId: bindingIds[0] ?? "", weight: "0", direction: "larger_is_better" }] })}>添加评分分量</Button>
          <p className="muted">权重之和必须为 1；缺失分量保留其权重，不会重新分配。</p>
        </div>
      )}

      <div className="form-grid">
        <Field label="数量选择" id="selection-mode">
          <AntSelect
            id="selection-mode"
            value={draft.selectionMode}
            onChange={(value: ScreenerDraft["selectionMode"]) => onChange({ ...draft, selectionMode: value })}
            options={[{ value: "all", label: "全部合格" }, { value: "top_n", label: "前 N 名" }]}
            aria-label="数量选择"
          />
        </Field>
        {draft.selectionMode === "top_n" ? (
          <Field label="N" id="selection-top-n" help="不足 N 个时返回全部合格标的，不会补入不合格标的。">
            <Input id="selection-top-n" value={draft.topN} className="mono" onChange={(event) => onChange({ ...draft, topN: event.target.value })} />
          </Field>
        ) : null}
      </div>
    </div>
  );
}

const CONDITION_KINDS = [
  { value: "compare", label: "比较 compare" },
  { value: "range", label: "区间 range" },
  { value: "set", label: "集合 set" },
  { value: "missing", label: "缺失 missing" },
  { value: "all", label: "全部满足 all" },
  { value: "any", label: "任一满足 any" },
  { value: "not", label: "取反 not" },
];

const COMPARE_OPERATORS = [
  { value: "gt", label: ">" },
  { value: "gte", label: "≥" },
  { value: "lt", label: "<" },
  { value: "lte", label: "≤" },
  { value: "eq", label: "=" },
  { value: "ne", label: "≠" },
];

const VALUE_KINDS = [
  { value: "decimal", label: "小数（字符串）" },
  { value: "number", label: "数字" },
  { value: "string", label: "文本" },
  { value: "boolean", label: "布尔" },
  { value: "timestamp", label: "时间戳" },
];

function replaceNode(node: NodeDraft, key: string, next: NodeDraft): NodeDraft {
  if (node.key === key) {
    return next;
  }
  return {
    ...node,
    children: node.children.map((child) => replaceNode(child, key, next)),
    child: node.child ? replaceNode(node.child, key, next) : null,
  };
}

function removeNode(node: NodeDraft, key: string): NodeDraft {
  return {
    ...node,
    children: node.children.filter((child) => child.key !== key).map((child) => removeNode(child, key)),
    child: node.child && node.child.key !== key ? removeNode(node.child, key) : null,
  };
}

function moveNode(children: NodeDraft[], index: number, offset: number): NodeDraft[] {
  const target = index + offset;
  const moved = children[index];
  if (!moved || target < 0 || target >= children.length) {
    return children;
  }
  const next = [...children];
  next.splice(index, 1);
  next.splice(target, 0, moved);
  return next;
}
