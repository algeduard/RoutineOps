import { Link, useLocation } from "react-router-dom"
import { ShieldAlert } from "lucide-react"
import { useMfaRequired } from "@/lib/mfaGate"
import { useT } from "@/lib/i18n"

// Баннер принуждения MFA (фича MFA enforce-by-policy, миграция 054). Показывается, когда
// орг-политика требует от текущего юзера включить 2FA, а он её ещё не включил (сигнал сервера
// через mfaGate). Мягко ведёт на /profile → секция «Двухфакторная аутентификация», а не запирает
// без объяснения. На самом /profile ссылку не рисуем — секция включения уже на странице.
const M = {
  title: { ru: "Требуется двухфакторная аутентификация", en: "Two-factor authentication required" },
  body: {
    ru: "Ваша организация требует включить 2FA для продолжения работы. Пока она не включена, действия в панели ограничены.",
    en: "Your organization requires 2FA to continue. Until it is enabled, actions in the console are restricted.",
  },
  cta: { ru: "Включить 2FA", en: "Enable 2FA" },
  here: { ru: "Включите её в разделе ниже.", en: "Enable it in the section below." },
}

export default function MfaRequiredBanner() {
  const t = useT()
  const required = useMfaRequired()
  const location = useLocation()
  if (!required) return null
  const onProfile = location.pathname.startsWith("/profile")

  return (
    <div
      role="alert"
      className="mb-5 flex items-start gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-amber-900 dark:text-amber-200"
    >
      <ShieldAlert className="h-5 w-5 flex-shrink-0 mt-0.5 text-amber-600 dark:text-amber-400" />
      <div className="flex-1 min-w-0">
        <p className="text-sm font-semibold">{t(M.title)}</p>
        <p className="text-xs mt-0.5 opacity-90">
          {t(M.body)}{onProfile ? " " + t(M.here) : ""}
        </p>
      </div>
      {!onProfile && (
        <Link
          to="/profile"
          className="flex-shrink-0 self-center rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white transition-colors hover:bg-amber-700"
        >
          {t(M.cta)}
        </Link>
      )}
    </div>
  )
}
