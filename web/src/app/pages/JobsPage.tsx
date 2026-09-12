import { Alert, Button, Modal } from "antd";
import { useCallback, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useCancelJob, useJob, useJobs, useRetryJob, type Job } from "../../api/hooks";
import { useJobEventStream } from "../../api/sse";
import { EmptyState, IssueList, JobProgress, LoadMore, PageHeader, QueryError, QueryLoading, Section, StatusBadge, formatDateTime } from "../ui";

const streamLabels = {
  connecting: "正在连接事件流",
  connected: "事件流已连接",
  reconnecting: "事件流断开，准备重连",
  expired: "事件窗口已过期，正在从最新状态恢复",
  unavailable: "事件流暂不可用，保留轮询状态",
} as const;

export function JobsPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedId = searchParams.get("job") ?? undefined;
  const jobs = useJobs();
  const selectedJobQuery = useJob(selectedId);
  const queryClient = useQueryClient();
  const cancelJob = useCancelJob();
  const retryJob = useRetryJob();
  const [cancelTarget, setCancelTarget] = useState<Job | undefined>();
  const [cancelError, setCancelError] = useState("");
  const [feedback, setFeedback] = useState("");
  const [streamStatus, setStreamStatus] = useState<keyof typeof streamLabels>("connecting");
  const items = useMemo(() => jobs.data?.pages.flatMap((page) => page.items) ?? [], [jobs.data?.pages]);
  const selectedJob = selectedJobQuery.data ?? items.find((job) => job.id === selectedId);

  const handleStreamJob = useCallback((job: Job) => {
    queryClient.setQueryData(["job", job.id], job);
    void jobs.refetch();
  }, [jobs, queryClient]);
  const handleStreamStatus = useCallback((status: keyof typeof streamLabels) => setStreamStatus(status), []);
  useJobEventStream(selectedId, handleStreamJob, handleStreamStatus);

  function selectJob(id: string) {
    const next = new URLSearchParams(searchParams);
    next.set("job", id);
    setSearchParams(next);
  }

  function closeDetail() {
    const next = new URLSearchParams(searchParams);
    next.delete("job");
    setSearchParams(next);
  }

  async function confirmCancel() {
    if (!cancelTarget) return;
    setCancelError("");
    try {
      await cancelJob.mutateAsync(cancelTarget.id);
      setFeedback(`任务 ${cancelTarget.id} 已请求取消；最终状态以 Worker 提交的终态为准。`);
      setCancelTarget(undefined);
    } catch {
      setCancelError("取消请求未完成。请保留对话框，检查服务状态后重试。");
    }
  }

  async function retry(id: string) {
    setFeedback("");
    try {
      const job = await retryJob.mutateAsync(id);
      setFeedback(`已创建重试任务 ${job.id}；原任务记录保持不变。`);
      selectJob(job.id);
    } catch {
      // The mutation error remains available in the inline error region below.
    }
  }

  return (
    <div className="page-stack">
      <PageHeader eyebrow="运行状态 / DURABLE JOBS" title="任务中心" description="任务会在服务端持久化并可恢复。202 只表示已受理；取消、重试和结果入口都以 Job 终态为准。" />
      {feedback ? <Alert type="success" showIcon title={feedback} role="status" /> : null}
      {cancelJob.isError ? <QueryError error={cancelJob.error} actionLabel="关闭错误" onRetry={() => { cancelJob.reset(); setCancelError(""); }} /> : null}
      {retryJob.isError ? <QueryError error={retryJob.error} actionLabel="关闭错误" onRetry={() => void retryJob.reset()} /> : null}
      <Section title="后台任务" description="活动任务每 2.5 秒刷新；选择一项查看事件流与结果引用。">
        {jobs.isPending ? <QueryLoading label="正在读取任务" /> : jobs.isError ? <QueryError error={jobs.error} onRetry={() => void jobs.refetch()} /> : items.length === 0 ? (
          <EmptyState title="还没有任务" description="从数据中心发起更新或快照发布后，任务会出现在这里。" action={<Link className="button-link primary-link" to="/data">去数据中心</Link>} />
        ) : (
          <>
            <div className="table-scroll">
              <table className="data-table jobs-table">
                <caption className="sr-only">后台任务列表</caption>
                <thead><tr><th scope="col">任务</th><th scope="col">状态</th><th scope="col">阶段 / 进度</th><th scope="col">更新时间</th><th scope="col">操作</th></tr></thead>
                <tbody>
                  {items.map((job) => (
                    <tr key={job.id} className={selectedId === job.id ? "selected-row" : ""}>
                      <th scope="row"><button type="button" className="table-link" onClick={() => selectJob(job.id)}>{job.kind}</button><small className="mono block">{job.id}</small></th>
                      <td><StatusBadge state={job.state} /></td>
                      <td><JobProgress job={job} /></td>
                      <td>{formatDateTime(job.updated_at)}</td>
                      <td><div className="row-actions"><Button type="link" onClick={() => selectJob(job.id)}>查看</Button>{job.state === "queued" || job.state === "running" ? <Button type="link" danger onClick={() => { setCancelError(""); setCancelTarget(job); }}>取消</Button> : null}{job.state === "failed" || job.state === "cancelled" ? <Button type="link" onClick={() => void retry(job.id)} loading={retryJob.isPending}>重试</Button> : null}</div></td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <LoadMore hasMore={Boolean(jobs.hasNextPage)} loading={jobs.isFetchingNextPage} onClick={() => void jobs.fetchNextPage()} />
          </>
        )}
      </Section>

      {selectedId ? (
        <Section title="任务详情" description="最新 Job 状态是业务事实；事件流只是恢复更新的传输方式。" action={<Button type="text" onClick={closeDetail}>关闭详情</Button>}>
          {selectedJobQuery.isPending && !selectedJob ? <QueryLoading label="正在读取任务详情" /> : selectedJobQuery.isError && !selectedJob ? <QueryError error={selectedJobQuery.error} onRetry={() => void selectedJobQuery.refetch()} /> : selectedJob ? <JobDetail job={selectedJob} streamStatus={streamStatus} onCancel={() => { setCancelError(""); setCancelTarget(selectedJob); }} onRetry={() => void retry(selectedJob.id)} retrying={retryJob.isPending} /> : <EmptyState title="任务不存在" description="任务可能已被归档或当前用户无权访问。" />}
        </Section>
      ) : null}

      <Modal title="取消后台任务" open={Boolean(cancelTarget)} onCancel={() => { if (!cancelJob.isPending) setCancelTarget(undefined); }} onOk={() => void confirmCancel()} okText="请求取消" cancelText="继续运行" okButtonProps={{ danger: true, loading: cancelJob.isPending, disabled: cancelJob.isPending }} cancelButtonProps={{ disabled: cancelJob.isPending }}>
        <p>任务 <span className="mono">{cancelTarget?.id}</span> 将进入“正在取消”。Worker 到达安全点后才会提交“已取消”或“失败”终态。</p>
        {cancelError ? <Alert type="error" showIcon title={cancelError} role="alert" /> : null}
      </Modal>
    </div>
  );
}

function JobDetail({ job, streamStatus, onCancel, onRetry, retrying }: { job: Job; streamStatus: keyof typeof streamLabels; onCancel: () => void; onRetry: () => void; retrying: boolean }) {
  return (
    <div className="job-detail">
      <div className="job-detail-head">
        <div><p className="eyebrow">{job.kind}</p><h3 className="mono">{job.id}</h3></div>
        <div className="job-detail-actions">{job.state === "queued" || job.state === "running" ? <Button danger onClick={onCancel}>取消任务</Button> : null}{job.state === "failed" || job.state === "cancelled" ? <Button onClick={onRetry} loading={retrying}>创建重试任务</Button> : null}</div>
      </div>
      <div className="stream-status" role="status" aria-live="polite"><span className={`status-led ${streamStatus === "connected" ? "good" : "attention"}`} aria-hidden="true" />{streamLabels[streamStatus]} · 网络中断不会新建任务</div>
      <JobProgress job={job} />
      <dl className="detail-grid"><div><dt>创建时间</dt><dd>{formatDateTime(job.created_at)}</dd></div><div><dt>更新时间</dt><dd>{formatDateTime(job.updated_at)}</dd></div><div><dt>Run</dt><dd className="mono">{job.run_id ?? "—"}</dd></div><div><dt>父任务</dt><dd className="mono">{job.parent_job_id ?? "—"}</dd></div></dl>
      {job.error ? <div className="job-error"><h4>终态错误</h4><p>{job.error.message}</p><span className="mono">{job.error.code}{job.error.request_id ? ` · ${job.error.request_id}` : ""}</span><IssueList issues={job.error.issues} /></div> : null}
      <div className="result-refs"><h4>结果引用</h4>{job.result_refs.length ? <ul>{job.result_refs.map((result) => <li key={`${result.kind}-${result.id}`}><span>{result.kind}</span><span className="mono">{result.id}</span></li>)}</ul> : <p className="muted">任务完成后，结果引用会由服务端写入。</p>}</div>
    </div>
  );
}
