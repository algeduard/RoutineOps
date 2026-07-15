import { useState, useEffect, FormEvent } from "react"
import { useNavigate, useSearchParams, Link } from "react-router-dom"
import axios from "axios"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Card, CardHeader, CardTitle, CardContent } from "@/components/ui/card"

export default function AcceptInvite() {
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
      setError("Пароли не совпадают")
      return
    }
    if (password.length < 8) {
      setError("Минимум 8 символов")
      return
    }
    setLoading(true)
    try {
      await axios.post("/api/v1/auth/accept-invite", { token, name, password })
      navigate("/login")
    } catch {
      setError("Ошибка при создании аккаунта")
    } finally {
      setLoading(false)
    }
  }

  if (inviteValid === null) {
    return <div className="min-h-screen flex items-center justify-center bg-background"><p className="text-muted-foreground">Проверка приглашения...</p></div>
  }

  if (!inviteValid) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <Card className="w-full max-w-sm">
          <CardContent className="pt-6 space-y-2">
            <p className="text-destructive text-sm">Приглашение недействительно или истекло.</p>
            <Link to="/login" className="text-sm text-primary hover:underline block">На страницу входа</Link>
          </CardContent>
        </Card>
      </div>
    )
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Создание аккаунта</CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground mb-4">Email: <span className="font-medium text-foreground">{inviteEmail}</span></p>
          <form onSubmit={handleSubmit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="name">Имя</Label>
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
              <Label htmlFor="password">Пароль</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm">Подтвердите пароль</Label>
              <Input
                id="confirm"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                required
              />
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading}>
              {loading ? "Создание..." : "Создать аккаунт"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
