import { defineConfig } from "vitest/config"
import path from "path"

// Отдельный конфиг для тестов — vite.config.ts (сборка) не трогаем.
export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
  },
})
