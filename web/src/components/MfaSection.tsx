import { useState, useEffect, FormEvent } from "react"
import QRCode from "qrcode"
import api, { errMessage } from "@/lib/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Badge } from "@/components/ui/badge"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"

interface MfaStatus {
  enabled: boolean
  confirmed_at?: string
  recovery_codes_remaining: number
}

const M = {
  title: { ru: "Двухфакторная аутентификация", en: "Two-factor authentication" },
  sub: { ru: "Второй фактор при входе — TOTP-приложение (Google Authenticator, Authy, 1Password)", en: "A second factor at sign-in — a TOTP app (Google Authenticator, Authy, 1Password)" },
  statusOn: { ru: "Включена", en: "Enabled" },
  statusOff: { ru: "Выключена", en: "Disabled" },
  remaining: { ru: "Кодов восстановления осталось: {n}", en: "Recovery codes left: {n}" },
  lowRemaining: { ru: "Мало кодов восстановления — перевыпустите набор", en: "Few recovery codes left — regenerate the set" },
  enable: { ru: "Включить", en: "Enable" },
  disable: { ru: "Отключить", en: "Disable" },
  regenerate: { ru: "Перевыпустить коды восстановления", en: "Regenerate recovery codes" },
  scan: { ru: "Отсканируйте QR в приложении-аутентификаторе", en: "Scan the QR in your authenticator app" },
  manualEntry: { ru: "Или введите ключ вручную:", en: "Or enter the key manually:" },
  enterCode: { ru: "Код из приложения", en: "Code from the app" },
  confirm: { ru: "Подтвердить и включить", en: "Confirm and enable" },
  cancel: { ru: "Отмена", en: "Cancel" },
  working: { ru: "Подождите...", en: "Working..." },
  password: { ru: "Текущий пароль", en: "Current password" },
  code: { ru: "Код (TOTP или восстановления)", en: "Code (TOTP or recovery)" },
  disableTitle: { ru: "Отключить двухфакторную аутентификацию", en: "Disable two-factor authentication" },
  regenTitle: { ru: "Перевыпустить коды восстановления", en: "Regenerate recovery codes" },
  recoveryTitle: { ru: "Сохраните коды восстановления", en: "Save your recovery codes" },
  recoveryHint: { ru: "Показываются один раз. Каждый код одноразовый — храните в надёжном месте. Ими можно войти, если потеряете телефон.", en: "Shown once. Each code works a single time — store them safely. Use one to sign in if you lose your phone." },
  copy: { ru: "Копировать", en: "Copy" },
  copied: { ru: "Скопировано", en: "Copied" },
  download: { ru: "Скачать .txt", en: "Download .txt" },
  done: { ru: "Готово, я сохранил коды", en: "Done, I saved them" },
  enabledOk: { ru: "MFA включена", en: "MFA enabled" },
  disabledOk: { ru: "MFA отключена", en: "MFA disabled" },
  regeneratedOk: { ru: "Коды восстановления перевыпущены", en: "Recovery codes regenerated" },
  genericErr: { ru: "Ошибка", en: "Error" },
}

export default function MfaSection() {
  const t = useT()
  const [status, setStatus] = useState<MfaStatus | null>(null)
  const [enrolling, setEnrolling] = useState(false)
  const [qr, setQr] = useState("")
  const [secret, setSecret] = useState("")
  const [confirmCode, setConfirmCode] = useState("")
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null)
  const [disableOpen, setDisableOpen] = useState(false)
  const [regenOpen, setRegenOpen] = useState(false)
  const [pw, setPw] = useState("")
  const [code, setCode] = useState("")
  const [busy, setBusy] = useState(false)

  function loadStatus() {
    api.get<MfaStatus>("/me/mfa").then((r) => setStatus(r.data)).catch(() => {})
  }
  useEffect(loadStatus, [])

  async function startEnroll() {
    setBusy(true)
    try {
      const r = await api.post<{ otpauth_uri: string; secret_base32: string }>("/me/mfa/enroll")
      setQr(await QRCode.toDataURL(r.data.otpauth_uri, { margin: 1, width: 200 }))
      setSecret(r.data.secret_base32)
      setConfirmCode("")
      setEnrolling(true)
    } catch (e) {
      toast({ title: t(M.genericErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setBusy(false)
    }
  }

  async function confirmEnroll(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const r = await api.post<{ recovery_codes: string[] }>("/me/mfa/confirm", { code: confirmCode })
      setEnrolling(false)
      setRecoveryCodes(r.data.recovery_codes)
      toast({ title: t(M.enabledOk), variant: "success" })
      loadStatus()
    } catch (e) {
      toast({ title: t(M.genericErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setBusy(false)
    }
  }

  async function doDisable(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      await api.post("/me/mfa/disable", { password: pw, code })
      setDisableOpen(false)
      setPw(""); setCode("")
      toast({ title: t(M.disabledOk), variant: "success" })
      loadStatus()
    } catch (e) {
      toast({ title: t(M.genericErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setBusy(false)
    }
  }

  async function doRegen(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    try {
      const r = await api.post<{ recovery_codes: string[] }>("/me/mfa/recovery-codes", { password: pw, code })
      setRegenOpen(false)
      setPw(""); setCode("")
      setRecoveryCodes(r.data.recovery_codes)
      toast({ title: t(M.regeneratedOk), variant: "success" })
      loadStatus()
    } catch (e) {
      toast({ title: t(M.genericErr), description: errMessage(e), variant: "destructive" })
    } finally {
      setBusy(false)
    }
  }

  function copyCodes() {
    if (recoveryCodes) navigator.clipboard?.writeText(recoveryCodes.join("\n"))
    toast({ title: t(M.copied), variant: "success" })
  }
  function downloadCodes() {
    if (!recoveryCodes) return
    const blob = new Blob([recoveryCodes.join("\n") + "\n"], { type: "text/plain" })
    const url = URL.createObjectURL(blob)
    const a = document.createElement("a")
    a.href = url
    a.download = "routineops-recovery-codes.txt"
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className="glass px-5 py-[18px] flex flex-col gap-4">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h2 className="text-[15px] font-semibold text-foreground">{t(M.title)}</h2>
          <p className="text-xs text-muted-foreground">{t(M.sub)}</p>
        </div>
        {status && (
          <Badge variant={status.enabled ? "default" : "secondary"}>
            {status.enabled ? t(M.statusOn) : t(M.statusOff)}
          </Badge>
        )}
      </div>

      {/* Показ recovery-кодов ОДИН раз — перекрывает остальной UI секции. */}
      {recoveryCodes ? (
        <div className="flex flex-col gap-3">
          <div>
            <h3 className="text-sm font-semibold text-foreground">{t(M.recoveryTitle)}</h3>
            <p className="text-xs text-muted-foreground mt-1">{t(M.recoveryHint)}</p>
          </div>
          <div className="grid grid-cols-2 gap-1.5 rounded-md border border-border bg-muted/30 p-3 font-mono text-[13px] text-foreground">
            {recoveryCodes.map((c) => <span key={c} className="tabular-nums">{c}</span>)}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" size="sm" onClick={copyCodes}>{t(M.copy)}</Button>
            <Button type="button" variant="outline" size="sm" onClick={downloadCodes}>{t(M.download)}</Button>
            <Button type="button" size="sm" onClick={() => setRecoveryCodes(null)}>{t(M.done)}</Button>
          </div>
        </div>
      ) : enrolling ? (
        <form onSubmit={confirmEnroll} className="flex flex-col gap-3">
          <p className="text-xs text-muted-foreground">{t(M.scan)}</p>
          {qr && <img src={qr} alt="TOTP QR" width={180} height={180} className="rounded-md bg-white p-2 self-start" />}
          <div className="text-xs text-muted-foreground">
            {t(M.manualEntry)} <span className="font-mono text-foreground break-all">{secret}</span>
          </div>
          <div className="space-y-1.5">
            <Label className="text-soft">{t(M.enterCode)}</Label>
            <Input
              value={confirmCode}
              onChange={(e) => setConfirmCode(e.target.value)}
              required
              autoFocus
              inputMode="numeric"
              autoComplete="one-time-code"
              placeholder="000000"
              className="max-w-[160px]"
            />
          </div>
          <div className="flex gap-2">
            <Button type="submit" disabled={busy}>{busy ? t(M.working) : t(M.confirm)}</Button>
            <Button type="button" variant="outline" onClick={() => setEnrolling(false)}>{t(M.cancel)}</Button>
          </div>
        </form>
      ) : status?.enabled ? (
        <div className="flex flex-col gap-3">
          <p className={`text-xs ${status.recovery_codes_remaining <= 3 ? "text-destructive dark:text-[hsl(0_72%_66%)]" : "text-muted-foreground"}`}>
            {status.recovery_codes_remaining <= 3
              ? t(M.lowRemaining)
              : t(M.remaining, { n: String(status.recovery_codes_remaining) })}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button type="button" variant="outline" onClick={() => setRegenOpen(true)}>{t(M.regenerate)}</Button>
            <Button type="button" variant="destructive" onClick={() => setDisableOpen(true)}>{t(M.disable)}</Button>
          </div>
        </div>
      ) : (
        <Button type="button" className="self-start" disabled={busy} onClick={startEnroll}>
          {busy ? t(M.working) : t(M.enable)}
        </Button>
      )}

      {/* Диалог отключения: пароль + второй фактор. */}
      <Dialog open={disableOpen} onOpenChange={(o) => { setDisableOpen(o); if (!o) { setPw(""); setCode("") } }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t(M.disableTitle)}</DialogTitle></DialogHeader>
          <form onSubmit={doDisable} className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.password)}</Label>
              <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} required autoComplete="current-password" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.code)}</Label>
              <Input value={code} onChange={(e) => setCode(e.target.value)} required autoComplete="one-time-code" placeholder="000000" />
            </div>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setDisableOpen(false)}>{t(M.cancel)}</Button>
              <Button type="submit" variant="destructive" disabled={busy}>{busy ? t(M.working) : t(M.disable)}</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>

      {/* Диалог перевыпуска recovery-кодов: пароль + второй фактор. */}
      <Dialog open={regenOpen} onOpenChange={(o) => { setRegenOpen(o); if (!o) { setPw(""); setCode("") } }}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t(M.regenTitle)}</DialogTitle></DialogHeader>
          <form onSubmit={doRegen} className="space-y-4 pt-2">
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.password)}</Label>
              <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} required autoComplete="current-password" />
            </div>
            <div className="space-y-1.5">
              <Label className="text-soft">{t(M.code)}</Label>
              <Input value={code} onChange={(e) => setCode(e.target.value)} required autoComplete="one-time-code" placeholder="000000" />
            </div>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setRegenOpen(false)}>{t(M.cancel)}</Button>
              <Button type="submit" disabled={busy}>{busy ? t(M.working) : t(M.regenerate)}</Button>
            </div>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  )
}
