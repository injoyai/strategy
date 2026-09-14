import { Alert, Button, Empty, Input, Progress, Spin, Tag, type InputRef } from "antd";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Issue, Job } from "../api/hooks";
import { toApiError } from "../api/hooks";

export const stateLabels: Record<Job["state"], string> = {
  queued: "等待执行",
  running: "执行中",
  cancel_requested: "正在取消",
  succeeded: "已完成",
  failed: "失败",
  cancelled: "已取消",
};

const stateColors: Record<Job["state"], string> = {
  queued: "default",
  running: "processing",
  cancel_requested: "warning",
  succeeded: "success",
  failed: "error",
  cancelled: "default",
};

export function PageHeader({ eyebrow, title, description, action }: {
  eyebrow: string;
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h1>{title}</h1>
        <p className="page-description">{description}</p>
      </div>
      {action ? <div className="page-header-action">{action}</div> : null}
    </header>
  );
}

export function Section({ title, description, action, children, className = "" }: {
  title: string;
  description?: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <section className={`section-panel ${className}`}>
      <div className="section-heading">
        <div>
          <h2>{title}</h2>
          {description ? <p>{description}</p> : null}
        </div>
        {action ? <div className="section-action">{action}</div> : null}
      </div>
      {children}
    </section>
  );
}

export function SearchField({ value, onChange, placeholder }: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
}) {
  const id = useId();
  const inputRef = useRef<InputRef>(null);
  const [draft, setDraft] = useState(value);
  const [isComposing, setIsComposing] = useState(false);

  useEffect(() => setDraft(value), [value]);
  useEffect(() => {
    if (isComposing || draft === value) {
      return;
    }
    const timer = window.setTimeout(() => onChange(draft), 300);
    return () => window.clearTimeout(timer);
  }, [draft, isComposing, onChange, value]);

  function clear() {
    setDraft("");
    onChange("");
    inputRef.current?.input?.focus();
  }

  return (
    <div className="search-field">
      <label htmlFor={id}>搜索</label>
      <div className="search-control">
        <Input
          ref={inputRef}
          id={id}
          value={draft}
          placeholder={placeholder}
          onChange={(event) => setDraft(event.target.value)}
          onCompositionStart={() => setIsComposing(true)}
          onCompositionEnd={() => setIsComposing(false)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.nativeEvent.isComposing) {
              onChange(draft);
            }
          }}
          aria-label="搜索"
        />
        {draft ? (
          <Button type="text" className="clear-search" onClick={clear} aria-label="清除搜索">
            ×
          </Button>
        ) : null}
      </div>
    </div>
  );
}

export function Field({ label, id, help, children }: { label: string; id: string; help?: string; children: ReactNode }) {
  return <div className="field"><label htmlFor={id}>{label}</label>{children}{help ? <small id={`${id}-help`}>{help}</small> : null}</div>;
}

export function QueryLoading({ label = "正在读取服务端数据" }: { label?: string }) {
  return (
    <div className="query-state" role="status" aria-live="polite">
      <Spin size="small" />
      <span>{label}</span>
    </div>
  );
}

export function QueryError({ error, onRetry, actionLabel = "重新读取" }: { error: unknown; onRetry: () => void; actionLabel?: string }) {
  const apiError = toApiError(error);
  return (
    <Alert
      type={apiError.retryable ? "warning" : "error"}
      showIcon
      title={apiError.message}
      description={
        <div className="error-details">
          <span>错误码：{apiError.code}</span>
          {apiError.request_id ? <span>请求 ID：{apiError.request_id}</span> : null}
          <Button type="link" onClick={onRetry}>
            {actionLabel}
          </Button>
        </div>
      }
    />
  );
}

export function EmptyState({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
  return (
    <div className="empty-state">
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={false} />
      <h3>{title}</h3>
      <p>{description}</p>
      {action ? <div>{action}</div> : null}
    </div>
  );
}

export function StatusBadge({ state }: { state: Job["state"] }) {
  return <Tag color={stateColors[state]}>{stateLabels[state]}</Tag>;
}

export function JobProgress({ job }: { job: Job }) {
  const determinate = job.total !== null && job.total > 0;
  const percent = determinate ? Math.min(100, Math.round((job.completed / job.total!) * 100)) : 0;
  return (
    <div className="job-progress">
      <div className="job-progress-label">
        <span>{job.phase || "未开始"}</span>
        <span className="mono">
          {job.total === null ? `已处理 ${job.completed}` : `${job.completed} / ${job.total}`}
        </span>
      </div>
      {determinate ? <Progress percent={percent} showInfo={false} size="small" /> : <Progress percent={percent} showInfo={false} status="active" size="small" />}
    </div>
  );
}

export function IssueList({ issues }: { issues: Issue[] }) {
  if (issues.length === 0) {
    return <span className="muted">暂无质量问题</span>;
  }
  return (
    <ul className="issue-list">
      {issues.map((issue) => (
        <li key={`${issue.code}-${issue.path}-${issue.message}`}>
          <span className={`issue-dot issue-${issue.severity}`} aria-hidden="true" />
          <span>
            <strong>{issue.code}</strong>
            {issue.path ? <span className="mono issue-path">{issue.path}</span> : null}
            <span>{issue.message}</span>
          </span>
        </li>
      ))}
    </ul>
  );
}

export function LoadMore({ hasMore, loading, onClick }: { hasMore: boolean; loading: boolean; onClick: () => void }) {
  if (!hasMore) {
    return null;
  }
  return (
    <div className="load-more">
      <Button onClick={onClick} loading={loading} disabled={loading}>
        加载更多
      </Button>
    </div>
  );
}

/**
 * Formats a server timestamp for display. The time zone is named explicitly
 * because a decision time without its zone is not an answer; the component
 * options are spelled out rather than using dateStyle/timeStyle, which the
 * spec (and V8) refuse to combine with timeZoneName.
 */
export function formatDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    timeZoneName: "short",
  }).format(date);
}

export function Metric({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <div className="metric">
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}
