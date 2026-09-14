import { Layout } from "antd";
import { useEffect } from "react";
import { NavLink, Navigate, Outlet, Route, Routes, useLocation } from "react-router";
import { ConnectionsPage } from "./pages/ConnectionsPage";
import { DataPage } from "./pages/DataPage";
import { FactorsPage } from "./pages/FactorsPage";
import { JobsPage } from "./pages/JobsPage";
import { ScreenRunPage } from "./pages/ScreenRunPage";
import { ScreenerDetailPage } from "./pages/ScreenerDetailPage";
import { ScreenerEditorPage, ScreenersPage } from "./pages/ScreenersPage";
import { UniversesPage } from "./pages/UniversesPage";

const navItems = [
  { to: "/data", label: "数据中心", glyph: "◫", section: "研究输入" },
  { to: "/universes", label: "标的池", glyph: "⊞", section: "研究输入" },
  { to: "/factors", label: "因子库", glyph: "∿", section: "研究输入" },
  { to: "/screeners", label: "选股方案", glyph: "⌗", section: "选股" },
  { to: "/connections", label: "数据源", glyph: "⌁", section: "研究输入" },
  { to: "/jobs", label: "任务中心", glyph: "◌", section: "运行状态" },
];

const plannedItems = [
  { label: "策略工作台", gate: "M2" },
  { label: "历史复盘", gate: "M2R" },
];

const titles: Record<string, string> = {
  "/data": "数据中心",
  "/universes": "标的池",
  "/factors": "因子库",
  "/screeners": "选股方案",
  "/screeners/new": "新建选股方案",
  "/screen-runs": "选股运行",
  "/connections": "数据源",
  "/jobs": "任务中心",
};

export function AppShell() {
  return (
    <Layout className="app-layout">
      <aside className="app-sidebar" aria-label="主导航">
        <div className="brand-lockup">
          <span className="brand-mark" aria-hidden="true">∷</span>
          <div>
            <strong>研究台</strong>
            <span>RESEARCH / M1</span>
          </div>
        </div>
        <nav aria-label="工作区">
          <p className="nav-group-label">工作区</p>
          {navItems.map((item) => (
            <NavLink key={item.to} to={item.to} className={({ isActive }) => `nav-link${isActive ? " active" : ""}`}>
              <span className="nav-glyph" aria-hidden="true">{item.glyph}</span>
              <span>{item.label}</span>
            </NavLink>
          ))}
          <p className="nav-group-label planned-label">后续阶段</p>
          {plannedItems.map((item) => (
            <div className="nav-link planned" key={item.label} aria-disabled="true">
              <span className="nav-glyph" aria-hidden="true">·</span>
              <span>{item.label}</span>
              <small>{item.gate}</small>
            </div>
          ))}
        </nav>
        <div className="sidebar-footer">
          <span className="status-led neutral" aria-hidden="true" />
          <div>
            <strong>本地实验环境</strong>
            <span>SQLite · 状态持久化</span>
          </div>
        </div>
      </aside>
      <Layout className="app-main-layout">
        <header className="app-header">
          <div className="header-context">
            <span className="header-kicker">STRATEGY RESEARCH WORKSPACE</span>
            <span className="header-divider" aria-hidden="true" />
            <span>M1 · 数据证据、标的池与因子</span>
          </div>
          <div className="header-status">
            <span className="status-led neutral" aria-hidden="true" />
            <span>服务状态由 API 返回</span>
          </div>
        </header>
        <main className="app-content" id="main-content">
          <Routes>
            <Route element={<RouteFrame />}>
              <Route path="/" element={<Navigate to="/data" replace />} />
              <Route path="/data" element={<DataPage />} />
              <Route path="/universes" element={<UniversesPage />} />
              <Route path="/factors" element={<FactorsPage />} />
              <Route path="/factors/:id" element={<FactorsPage />} />
              <Route path="/screeners" element={<ScreenersPage />} />
              <Route path="/screeners/new" element={<ScreenerEditorPage />} />
              <Route path="/screeners/:id" element={<ScreenerDetailPage />} />
              <Route path="/screen-runs/:id" element={<ScreenRunPage />} />
              <Route path="/connections" element={<ConnectionsPage />} />
              <Route path="/jobs" element={<JobsPage />} />
              <Route path="*" element={<NotFoundPage />} />
            </Route>
          </Routes>
        </main>
        <footer className="app-footer">
          <span>Research platform · M1 data, universes and factors</span>
          <span className="mono">PIT / immutable snapshots / registered factors</span>
        </footer>
      </Layout>
    </Layout>
  );
}

function RouteFrame() {
  const location = useLocation();
  useEffect(() => {
    // Detail routes share the section title (/factors/:id falls back to /factors).
    const section = `/${location.pathname.split("/")[1] ?? ""}`;
    document.title = `${titles[location.pathname] ?? titles[section] ?? "页面未找到"} — 策略研究工作台`;
  }, [location.pathname]);
  return <Outlet />;
}

function NotFoundPage() {
  return (
    <section className="route-message">
      <p className="eyebrow">404 · ROUTE NOT FOUND</p>
      <h1>这个页面还不存在</h1>
      <p>返回数据中心，继续查看真实的研究输入与任务状态。</p>
      <NavLink className="button-link primary-link" to="/data">回到数据中心</NavLink>
    </section>
  );
}
