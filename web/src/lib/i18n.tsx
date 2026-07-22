import { useSyncExternalStore } from "react"

// Лёгкая локализация без внешних зависимостей. Подход — КОЛОКЕЙШН: строки живут
// рядом с компонентом как объекты {ru, en}, компонент зовёт t(M.key). Общего
// словаря нет, поэтому файлы переводятся независимо (без конфликтов). Язык —
// глобальный, с подпиской через useSyncExternalStore; по умолчанию русский.

export type Lang = "ru" | "en"
export type Msg = { ru: string; en: string }

const STORAGE_KEY = "lang"
let currentLang: Lang = readInitial()
const listeners = new Set<() => void>()

function readInitial(): Lang {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v === "en" || v === "ru") return v
  } catch {
    /* localStorage недоступен — дефолт ru */
  }
  return "ru"
}

export function getLang(): Lang {
  return currentLang
}

export function setLang(l: Lang) {
  if (l === currentLang) return
  currentLang = l
  try {
    localStorage.setItem(STORAGE_KEY, l)
  } catch {
    /* ignore */
  }
  try {
    document.documentElement.lang = l
  } catch {
    /* ignore */
  }
  listeners.forEach((f) => f())
}

function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => {
    listeners.delete(cb)
  }
}

export function useLang(): Lang {
  return useSyncExternalStore(subscribe, () => currentLang, () => currentLang)
}

// t выбирает строку под текущий язык и подставляет плейсхолдеры {name}. Читает язык
// в момент вызова — компонент обязан подписаться на смену языка через useT()/useLang(),
// иначе не перерисуется при переключении.
export function t(m: Msg, vars?: Record<string, string | number>): string {
  let s = m[currentLang] ?? m.ru
  if (vars) {
    for (const k of Object.keys(vars)) s = s.split(`{${k}}`).join(String(vars[k]))
  }
  return s
}

// useT подписывает компонент на смену языка и возвращает t. Использование:
//   const t = useT(); ... <h1>{t(M.title)}</h1>
export function useT() {
  useLang()
  return t
}
