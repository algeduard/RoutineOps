import { useState, useEffect, FormEvent } from "react"
import { useNavigate, useSearchParams, Link } from "react-router-dom"
import axios from "axios"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card"
import { RoutineOpsLogo } from "@/components/RoutineOpsLogo"
import SpotlightCard from "@/components/SpotlightCard"
import { useT } from "@/lib/i18n"

const M = {
  passwordsDontMatch: { ru: "Пароли не совпадают", en: "Passwords do not match" },
  minChars: { ru: "Минимум 8 символов", en: "Minimum 8 characters" },
  accountCreateError: { ru: "Ошибка при создании аккаунта", en: "Failed to create account" },
  checkingInvite: { ru: "Проверка приглашения...", en: "Checking invitation..." },
  inviteInvalid: { ru: "Приглашение недействительно или истекло.", en: "The invitation is invalid or has expired." },
  toLogin: { ru: "На страницу входа", en: "To sign in" },
  createAccountTitle: { ru: "Создание аккаунта", en: "Create account" },
  emailLabel: { ru: "Email:", en: "Email:" },
  name: { ru: "Имя", en: "Name" },
  password: { ru: "Пароль", en: "Password" },
  confirmPassword: { ru: "Подтвердите пароль", en: "Confirm password" },
  creating: { ru: "Создание...", en: "Creating..." },
  createAccountBtn: { ru: "Создать аккаунт", en: "Create account" },
}

export default function AcceptInvite() {
  const t = useT()
  const [searchParams] = useSearchParams()
  const token = searchParams.get("token") ?? ""
  const navigate = useNavigate()

  const [inviteEmail, setInviteEmail] = useState("")
  const [inviteValid, setInviteValid] = useState<boolean | null>(null)
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!token) {
      setInviteValid(false)
      return
    }
    const controller = new AbortController()
    axios.get(`/api/v1/auth/invite?token=${token}`, { signal: controller.signal })
      .then((r) => {
        setInviteEmail(r.data.email)
        setInviteValid(true)
      })
      .catch((err) => { if (!axios.isCancel(err)) setInviteValid(false) })
    return () => controller.abort()
  }, [token])

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setError("")
    if (password !== confirm) {
      setError(t(M.passwordsDontMatch))
      return
    }
    if (password.length < 8) {
      setError(t(M.minChars))
      return
    }
    setLoading(true)
    try {
      await axios.post("/api/v1/auth/accept-invite", { token, name, password })
      navigate("/login")
    } catch {
      setError(t(M.accountCreateError))
    } finally {
      setLoading(false)
    }
  }

  if (inviteValid === null) {
    return <div className="min-h-screen flex items-center justify-center p-4"><p className="text-sm text-muted-foreground">{t(M.checkingInvite)}</p></div>
  }

  if (!inviteValid) {
    return (
      // Без bg-background: карта стоит прямо на фоне body с радиальными бликами.
      <div className="min-h-screen flex items-center justify-center p-4">
        <Card className="w-full max-w-sm">
          <CardContent className="px-5 py-[18px] space-y-2">
            {/* --destructive в тёмной теме (45% светлоты) на стекле почти не читается —
                берём тот же красный, что у алерт-цифры на дашборде. */}
            <p className="text-sm text-destructive dark:text-[hsl(0_72%_66%)]">{t(M.inviteInvalid)}</p>
            <Link to="/login" className="text-sm text-brand hover:underline block">{t(M.toLogin)}</Link>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    // Без bg-background: карта стоит прямо на фоне body с радиальными бликами.
    <div className="min-h-screen flex items-center justify-center p-4">
      <SpotlightCard as={Card} className="w-full max-w-sm">
        <CardHeader className="px-5 pt-6 pb-2">
          <CardTitle className="flex items-center justify-center gap-2.5 py-2 text-foreground">
            <RoutineOpsLogo size={32} />
            <span className="text-lg font-semibold tracking-tight">{t(M.createAccountTitle)}</span>
          </CardTitle>
        </CardHeader>
        <CardContent className="px-5 pb-6">
          <p className="text-sm text-muted-foreground mb-4">{t(M.emailLabel)} <span className="font-medium text-foreground">{inviteEmail}</span></p>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="name" className="text-soft">{t(M.name)}</Label>
              <Input
                id="name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
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
            <div className="space-y-1.5">
              <Label htmlFor="confirm" className="text-soft">{t(M.confirmPassword)}</Label>
              <Input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
              />
            </div>
            {/* --destructive в тёмной теме (45% светлоты) на стекле почти не читается —
                берём тот же красный, что у алерт-цифры на дашборде. */}
            {error && <p className="text-sm text-destructive dark:text-[hsl(0_72%_66%)]">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? t(M.creating) : t(M.createAccountBtn)}
            </Button>
          </form>
        </CardContent>
      </SpotlightCard>
    </div>
  )
}
