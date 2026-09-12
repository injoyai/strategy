import type { ThemeConfig } from "antd";

/**
 * Runtime adapter for DESIGN.md. Keep semantic roles here; components should
 * consume Ant Design tokens or CSS variables instead of copying hex values.
 */
export const tokens = {
  color: {
    canvas: "#F3F6FA",
    surface: "#FFFFFF",
    text: "#172B4D",
    secondaryText: "#52627A",
    primary: "#2457C5",
    border: "#D6DEE9",
    success: "#18794E",
    warning: "#8A5700",
    danger: "#B42318",
  },
  radius: {
    control: 6,
    panel: 8,
  },
  spacing: {
    unit: 4,
  },
  typography: {
    body: '"Segoe UI", "Microsoft YaHei", sans-serif',
    mono: '"Cascadia Mono", Consolas, monospace',
  },
} as const;

export const antdTheme: ThemeConfig = {
  token: {
    colorPrimary: tokens.color.primary,
    colorText: tokens.color.text,
    colorTextSecondary: tokens.color.secondaryText,
    colorBorder: tokens.color.border,
    colorBgLayout: tokens.color.canvas,
    colorBgContainer: tokens.color.surface,
    borderRadius: tokens.radius.control,
    fontFamily: tokens.typography.body,
    fontSize: 14,
    controlHeight: 40,
  },
  components: {
    Button: { borderRadius: tokens.radius.control, controlHeight: 40 },
    Input: { borderRadius: tokens.radius.control, controlHeight: 40 },
    Select: { borderRadius: tokens.radius.control, controlHeight: 40 },
    Progress: { defaultColor: tokens.color.primary },
  },
};
