import { useState, FormEvent } from "react"
import api, { errMessage } from "@/lib/api"
import { useMe } from "@/lib/useMe"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { toast } from "@/lib/toast"

const roleLabels: Record<string, string> = {
  it_admin: "IT-администратор",
  viewer: "Наблюдатель",
}

export default function Profile() {
  const { me } = useMe()
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (next !== confirm) {
      toast({ title: "Новые пароли не совпадают", variant: "destructive" })
      return
    }
    setLoading(true)
    try {
      await api.post("/me/password", { current_password: current, new_password: next })
      toast({ title: "Пароль изменён", variant: "success" })
      setCurrent("")
      setNext("")
      setConfirm("")
    } catch (e) {
      toast({ title: "Не удалось сменить пароль", description: errMessage(e), variant: "destructive" })
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="p-6 space-y-6 max-w-lg">
      <h1 className="text-xl font-semibold">Профиль</h1>

      <div className="space-y-2 text-sm rounded-lg border p-4">
        <div><span className="text-muted-foreground">Имя: </span>{me?.name ?? "—"}</div>
        <div><span className="text-muted-foreground">Email: </span>{me?.email ?? "—"}</div>
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground">Роль:</span>
          {me && <Badge variant={me.role === "it_admin" ? "default" : "secondary"}>{roleLabels[me.role] ?? me.role}</Badge>}
        </div>
      </div>

      <form onSubmit={handleSubmit} className="space-y-4">
        <h2 className="text-sm font-medium">Смена пароля</h2>
        <div className="space-y-1.5">
          <Label>Текущий пароль</Label>
          <Input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} required autoComplete="current-password" />
        </div>
        <div className="space-y-1.5">
          <Label>Новый пароль</Label>
          <Input type="password" value={next} onChange={(e) => setNext(e.target.value)} required autoComplete="new-password" />
        </div>
        <div className="space-y-1.5">
          <Label>Повторите новый пароль</Label>
          <Input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required autoComplete="new-password" />
        </div>
        <Button type="submit" disabled={loading}>{loading ? "Сохранение..." : "Сменить пароль"}</Button>
      </form>
    </div>
  )
}
