import { Alert, Button, Input, Select as AntSelect } from "antd";
import { useMemo, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { useConnections, useCreateConnection, useProviders } from "../../api/hooks";
import { EmptyState, LoadMore, PageHeader, QueryError, QueryLoading, SearchField, Section, formatDateTime } from "../ui";

type ConnectionDraft = {
  name: string;
  providerKey: string;
  settings: string;
  secretRef: string;
};

const initialDraft: ConnectionDraft = {
  name: "",
  providerKey: "",
  settings: "",
  secretRef: "",
};

export function ConnectionsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const query = searchParams.get("q") ?? "";
  const [draft, setDraft] = useState(initialDraft);
  const [formError, setFormError] = useState("");
  const [successMessage, setSuccessMessage] = useState("");
  const providers = useProviders();
  const connections = useConnections(query);
  const createConnection = useCreateConnection();
  const connectionItems = useMemo(() => connections.data?.pages.flatMap((page) => page.items) ?? [], [connections.data?.pages]);
  const providerItems = providers.data?.items ?? [];
  const selectedProvider = providerItems.find((provider) => `${provider.id}::${provider.version}` === draft.providerKey);

  function setQuery(nextQuery: string) {
    const next = new URLSearchParams(searchParams);
    if (nextQuery) {
      next.set("q", nextQuery);
    } else {
      next.delete("q");
    }
    setSearchParams(next);
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setFormError("");
    setSuccessMessage("");
    if (!draft.name.trim()) {
      setFormError("请填写连接名称。");
      document.getElementById("connection-name")?.focus();
      return;
    }
    if (!selectedProvider) {
      setFormError("请选择已注册的数据源版本。");
      document.getElementById("connection-provider")?.focus();
      return;
    }
    const settings = parseObject(draft.settings);
    if (!settings) {
      setFormError("参数必须是 JSON 对象，例如 {\"endpoint\": \"...\"}；不接受数组或其他类型。");
      document.getElementById("connection-settings")?.focus();
      return;
    }
    try {
      const connection = await createConnection.mutateAsync({
        name: draft.name.trim(),
        provider_ref: { id: selectedProvider.id, version: selectedProvider.version },
        settings,
        ...(draft.secretRef.trim() ? { secret_ref: draft.secretRef.trim() } : {}),
      });
      setDraft(initialDraft);
      setSuccessMessage(`连接版本 ${connection.id} 已创建。连接是不可变版本，后续修改应创建新版本。`);
    } catch {
      // The mutation error is rendered below with the normalized contract envelope.
    }
  }

  return (
    <div className="page-stack">
      <PageHeader
        eyebrow="研究输入 / DATA SOURCES"
        title="数据源"
        description="先确认服务端注册的能力，再创建不可变连接版本。密钥只接受后端返回的 secret_ref，不在浏览器回显明文。"
      />

      <Section title="已注册能力" description="能力、字段和 PIT 等级来自 Provider API；页面不推断市场覆盖。">
        {providers.isPending ? <QueryLoading label="正在读取数据源能力" /> : providers.isError ? <QueryError error={providers.error} onRetry={() => void providers.refetch()} /> : providerItems.length === 0 ? (
          <EmptyState title="还没有注册数据源" description="注册 Provider 后，这里会显示可用字段、频率和时点证据。" />
        ) : (
          <div className="provider-grid">
            {providerItems.map((provider) => (
              <article className="provider-card" key={`${provider.id}-${provider.version}`}>
                <div className="provider-card-heading">
                  <div>
                    <h3>{provider.name}</h3>
                    <span className="mono">{provider.id} · v{provider.version}</span>
                  </div>
                  <span className="soft-label">{provider.capabilities.length} 项能力</span>
                </div>
                <div className="capability-list">
                  {provider.capabilities.map((capability) => (
                    <div className="capability-row" key={`${capability.dataset}-${capability.pit_level}`}>
                      <span>{capability.dataset}</span>
                      <span className="mono">{capability.frequencies.join(" / ") || "未声明频率"}</span>
                      <span className={`pit-level pit-${capability.pit_level}`}>{pitLabel(capability.pit_level)}</span>
                    </div>
                  ))}
                </div>
              </article>
            ))}
          </div>
        )}
      </Section>

      <div className="two-column-grid">
        <Section title="创建连接版本" description="创建后不可原地修改；参数 schema 由选中的 Provider 约束。">
          <form className="stack-form" noValidate onSubmit={(event) => void handleSubmit(event)}>
            <div className="field">
              <label htmlFor="connection-name">连接名称</label>
              <Input id="connection-name" value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} aria-describedby="connection-name-help" />
              <small id="connection-name-help">使用团队能识别的名称，不要把密钥写进名称。</small>
            </div>
            <div className="field">
              <label htmlFor="connection-provider">Provider 版本</label>
              <AntSelect
                id="connection-provider"
                value={draft.providerKey || null}
                placeholder="选择注册版本"
                onChange={(value: string) => setDraft({ ...draft, providerKey: value })}
                options={providerItems.map((provider) => ({
                  value: `${provider.id}::${provider.version}`,
                  label: `${provider.name} · ${provider.id} · v${provider.version}`,
                }))}
                aria-label="Provider 版本"
              />
            </div>
            {selectedProvider ? (
              <div className="schema-note">
                <span className="eyebrow">CONFIGURATION SCHEMA</span>
                <p>请按 Provider 注册 schema 填写 JSON。未知关键字由后端拒绝，页面不会静默忽略。</p>
                <pre>{JSON.stringify(selectedProvider.config_schema, null, 2)}</pre>
              </div>
            ) : null}
            <div className="field">
              <label htmlFor="connection-settings">参数 JSON</label>
              <Input.TextArea id="connection-settings" value={draft.settings} onChange={(event) => setDraft({ ...draft, settings: event.target.value })} rows={5} aria-describedby="connection-settings-help" />
              <small id="connection-settings-help">仅填写参数对象；不要填写任意代码或本地文件路径。</small>
            </div>
            <div className="field">
              <label htmlFor="connection-secret-ref">secret_ref（可选）</label>
              <Input id="connection-secret-ref" value={draft.secretRef} onChange={(event) => setDraft({ ...draft, secretRef: event.target.value })} aria-describedby="connection-secret-help" />
              <small id="connection-secret-help">这里只保存引用标识，不读取、不显示密钥明文。</small>
            </div>
            {formError ? <Alert type="error" showIcon title={formError} role="alert" /> : null}
            {createConnection.isError ? <QueryError error={createConnection.error} actionLabel="关闭错误并继续编辑" onRetry={() => createConnection.reset()} /> : null}
            {successMessage ? <Alert type="success" showIcon title={successMessage} role="status" /> : null}
            <div className="form-actions">
              <Button type="primary" htmlType="submit" loading={createConnection.isPending} disabled={createConnection.isPending}>
                创建连接版本
              </Button>
              <span className="muted">提交自动带 Idempotency-Key</span>
            </div>
          </form>
        </Section>

        <Section title="现有连接" description="搜索条件保存在 URL，便于刷新和分享；列表使用服务端游标分页。">
          <div className="table-toolbar">
            <SearchField value={query} onChange={setQuery} placeholder="按名称或 ID 搜索" />
            <span className="result-count">{connectionItems.length} 条已加载</span>
          </div>
          {connections.isPending ? <QueryLoading label="正在读取连接" /> : connections.isError ? <QueryError error={connections.error} onRetry={() => void connections.refetch()} /> : connectionItems.length === 0 ? (
            <EmptyState title={query ? "没有匹配连接" : "尚未创建连接"} description={query ? "清除搜索条件后查看全部连接。" : "使用左侧表单创建第一个不可变连接版本。"} />
          ) : (
            <>
              <div className="table-scroll">
                <table className="data-table">
                  <caption className="sr-only">现有连接列表</caption>
                  <thead>
                    <tr><th scope="col">名称</th><th scope="col">Provider</th><th scope="col">版本</th><th scope="col">创建时间</th></tr>
                  </thead>
                  <tbody>
                    {connectionItems.map((connection) => (
                      <tr key={connection.id}>
                        <th scope="row">{connection.name}</th>
                        <td className="mono">{connection.provider_ref.id}</td>
                        <td className="mono">{connection.version}</td>
                        <td>{formatDateTime(connection.created_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <LoadMore hasMore={Boolean(connections.hasNextPage)} loading={connections.isFetchingNextPage} onClick={() => void connections.fetchNextPage()} />
            </>
          )}
        </Section>
      </div>
      <p className="page-footnote">还没有连接？先确认 Provider 能力；如果后端尚未注册 Provider，请联系部署维护者，而不是在前端创建示例供应商。</p>
      <Link className="quiet-link" to="/data">继续到数据中心 →</Link>
    </div>
  );
}

function parseObject(value: string): Record<string, unknown> | null {
  if (!value.trim()) {
    return null;
  }
  try {
    const parsed: unknown = JSON.parse(value);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? parsed as Record<string, unknown> : null;
  } catch {
    return null;
  }
}

function pitLabel(value: "verified" | "date_only" | "unverified") {
  if (value === "verified") return "PIT 已验证";
  if (value === "date_only") return "仅日期";
  return "未验证";
}
