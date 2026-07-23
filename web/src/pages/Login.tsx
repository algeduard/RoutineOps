import { useState, useEffect, FormEvent } from "react"
import { useNavigate, Link } from "react-router-dom"
import axios from "axios"
import { login, loginMfa } from "@/lib/auth"
import { clearMeCache } from "@/lib/useMe"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card"
import { RoutineOpsLogo } from "@/components/RoutineOpsLogo"
import SpotlightCard from "@/components/SpotlightCard"
import { useT } from "@/lib/i18n"

const M = {
  invalidCredentials: { ru: "Неверный email или пароль", en: "Invalid email or password" },
  password: { ru: "Пароль", en: "Password" },
  signingIn: { ru: "Вход...", en: "Signing in..." },
  signIn: { ru: "Войти", en: "Sign in" },
  forgotPassword: { ru: "Забыли пароль?", en: "Forgot password?" },
  ssoButton: { ru: "Войти через SSO", en: "Sign in with SSO" },
  or: { ru: "или", en: "or" },
  ssoLanding: { ru: "Завершаем вход...", en: "Finishing sign-in..." },
  ssoFailedGeneric: { ru: "Не удалось войти через SSO", en: "SSO sign-in failed" },
  ssoEmailConflict: { ru: "Аккаунт с этим email уже существует — обратитесь к администратору", en: "An account with this email already exists — contact your administrator" },
  ssoNoAccount: { ru: "Для этого пользователя нет учётной записи", en: "No account exists for this user" },
  mfaTitle: { ru: "Двухфакторная аутентификация", en: "Two-factor authentication" },
  mfaCode: { ru: "Код из приложения", en: "Authenticator code" },
  mfaCodeHint: { ru: "Введите 6-значный код из приложения-аутентификатора", en: "Enter the 6-digit code from your authenticator app" },
  recoveryCode: { ru: "Код восстановления", en: "Recovery code" },
  recoveryHint: { ru: "Введите один из сохранённых кодов восстановления", en: "Enter one of your saved recovery codes" },
  useRecovery: { ru: "Использовать код восстановления", en: "Use a recovery code" },
  useAuthenticator: { ru: "Использовать код из приложения", en: "Use an authenticator code" },
  verify: { ru: "Подтвердить", en: "Verify" },
  verifying: { ru: "Проверка...", en: "Verifying..." },
  invalidCode: { ru: "Неверный код или срок входа истёк", en: "Invalid code or the sign-in expired" },
  back: { ru: "Назад", en: "Back" },
}

export default function Login() {
  const t = useT()
  const [email, setEmail] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  // Непусто ⇒ шаг-1 прошёл, включена MFA, ждём второй фактор.
  const [mfaToken, setMfaToken] = useState("")
  const [code, setCode] = useState("")
  const [useRecovery, setUseRecovery] = useState(false)
  const [ssoEnabled, setSsoEnabled] = useState(false)
  // ssoLanding: вернулись с успешного SSO-callback (?sso=1) — пробим /me и уходим в app.
  const [ssoLanding, setSsoLanding] = useState(false)
  const navigate = useNavigate()

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const ssoErr = params.get("sso_error")
    if (ssoErr) {
      setError(
        ssoErr === "email_conflict" ? t(M.ssoEmailConflict)
        : ssoErr === "no_account" ? t(M.ssoNoAccount)
        : t(M.ssoFailedGeneric),
      )
    }
    if (params.get("sso") === "1") {
      // Сессия уже в httpOnly-куке (её не читает JS). Подтверждаем /me raw-axios (мимо
      // 401-интерцептора), ставим клиентский маркер и уходим в приложение.
      setSsoLanding(true)
      axios.get("/api/v1/me")
        .then(() => {
          sessionStorage.setItem("session", "1")
          clearMeCache()
          navigate("/", { replace: true })
        })
        .catch(() => {
          setSsoLanding(false)
          setError(t(M.ssoFailedGeneric))
        })
    }
    // Показывать ли кнопку SSO (страница логина неаутентифицирована — /capabilities ей недоступен).
    axios.get<{ enabled: boolean }>("/api/v1/auth/sso/status")
      .then((r) => setSsoEnabled(!!r.data?.enabled))
      .catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError("")
    setLoading(true)
    try {
      const res = await login(email, password)
      if (res.mfaRequired) {
        setMfaToken(res.mfaToken)
        return
      }
      navigate("/dashboard")
    } catch {
      setError(t(M.invalidCredentials))
    } finally {
      setLoading(false)
    }
  }

  async function handleMfaSubmit(e: FormEvent) {
    e.preventDefault()
    setError("")
    setLoading(true)
    try {
      await loginMfa(mfaToken, code)
      navigate("/dashboard")
    } catch {
      setError(t(M.invalidCode))
    } finally {
      setLoading(false)
    }
  }

  function backToPassword() {
    setMfaToken("")
    setCode("")
    setUseRecovery(false)
    setError("")
  }

  const errorNode = error && (
    // --destructive в тёмной теме (45% светлоты) на стекле почти не читается —
    // берём тот же красный, что у алерт-цифры на дашборде.
    <p className="text-sm text-destructive dark:text-[hsl(0_72%_66%)]">{error}</p>
  )

  return (
    // Без bg-background: карта стоит прямо на фоне body с радиальными бликами.
    <div className="min-h-screen flex items-center justify-center p-4">
      <SpotlightCard as={Card} className="w-full max-w-sm">
        <CardHeader className="px-5 pt-6 pb-2">
          <CardTitle className="flex items-center justify-center gap-2.5 py-2 text-foreground">
            <RoutineOpsLogo size={32} />
            <span className="text-lg font-semibold tracking-tight">RoutineOps</span>
          </CardTitle>
        </CardHeader>
        <CardContent className="px-5 pb-6">
          {ssoLanding ? (
            <p className="text-center text-sm text-muted-foreground py-6">{t(M.ssoLanding)}</p>
          ) : !mfaToken ? (
            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="space-y-1.5">
                <Label htmlFor="email" className="text-soft">Email</Label>
                <Input
                  id="email"
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                  autoFocus
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="password" className="text-soft">{t(M.password)}</Label>
                <Input
                  id="password"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                />
              </div>
              {errorNode}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? t(M.signingIn) : t(M.signIn)}
              </Button>
              <Link to="/forgot-password" className="block text-center text-sm text-muted-foreground hover:text-foreground transition-colors">
                {t(M.forgotPassword)}
              </Link>
              {ssoEnabled && (
                <>
                  <div className="flex items-center gap-3 pt-1">
                    <div className="h-px flex-1 bg-border" />
                    <span className="text-xs text-muted-foreground">{t(M.or)}</span>
                    <div className="h-px flex-1 bg-border" />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    className="w-full"
                    onClick={() => { window.location.href = "/api/v1/auth/sso/login" }}
                  >
                    {t(M.ssoButton)}
                  </Button>
                </>
              )}
            </form>
          ) : (
            <form onSubmit={handleMfaSubmit} className="space-y-4">
              <div>
                <h2 className="text-[15px] font-semibold text-foreground text-center">{t(M.mfaTitle)}</h2>
                <p className="text-xs text-muted-foreground text-center mt-1">
                  {useRecovery ? t(M.recoveryHint) : t(M.mfaCodeHint)}
                </p>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="mfacode" className="text-soft">
                  {useRecovery ? t(M.recoveryCode) : t(M.mfaCode)}
                </Label>
                <Input
                  id="mfacode"
                  type="text"
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  required
                  autoFocus
                  autoComplete="one-time-code"
                  inputMode={useRecovery ? "text" : "numeric"}
                  placeholder={useRecovery ? "XXXX-XXXX-XXXX-XXXX-XXXX-XXXX" : "000000"}
                />
              </div>
              {errorNode}
              <Button type="submit" className="w-full" disabled={loading}>
                {loading ? t(M.verifying) : t(M.verify)}
              </Button>
              <div className="flex items-center justify-between text-sm">
                <button
                  type="button"
                  onClick={backToPassword}
                  className="text-muted-foreground hover:text-foreground transition-colors"
                >
                  {t(M.back)}
                </button>
                <button
                  type="button"
                  onClick={() => { setUseRecovery((v) => !v); setCode(""); setError("") }}
                  className="text-muted-foreground hover:text-foreground transition-colors"
                >
                  {useRecovery ? t(M.useAuthenticator) : t(M.useRecovery)}
                </button>
              </div>
            </form>
          )}
        </CardContent>
      </SpotlightCard>
    </div>
  )
}
