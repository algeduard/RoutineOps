import { useState } from "react"

export type Theme = "light" | "dark"

const STORAGE_KEY = "theme"

// getInitialTheme: сохранённый выбор пользователя, иначе системная тема.
export function getInitialTheme(): Theme {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === "light" || saved === "dark") return saved
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"
}

// applyTheme переключает класс .dark на <html> (Tailwind darkMode: class).
export function applyTheme(theme: Theme) {
  document.documentElement.classList.toggle("dark", theme === "dark")
}

// Применяем тему сразу при импорте модуля — до первого рендера React,
// чтобы не было вспышки светлой темы (FOUC). main.tsx импортирует модуль рано.
applyTheme(getInitialTheme())

// useTheme — состояние темы + переключение с сохранением в localStorage.
export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(getInitialTheme)

  function toggleTheme() {
    const next: Theme = theme === "dark" ? "light" : "dark"
    localStorage.setItem(STORAGE_KEY, next)
    applyTheme(next)
    setThemeState(next)
  }

  return { theme, toggleTheme }
}
