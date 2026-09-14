import { ConfigProvider } from "antd";
import zhCN from "antd/locale/zh_CN";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import { AppShell } from "./app/AppShell";
import { antdTheme } from "./theme/tokens";

// Module-scoped so the cache survives navigation; exported so tests can clear
// it between cases instead of reading a previous case's cached response.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: true,
      staleTime: 5_000,
    },
  },
});

export default function App() {
  return (
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <ConfigProvider locale={zhCN} theme={antdTheme}>
          <AppShell />
        </ConfigProvider>
      </QueryClientProvider>
    </BrowserRouter>
  );
}
