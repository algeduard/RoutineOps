import { useEffect, useState } from "react"
import { Outlet, NavLink, useNavigate, useLocation } from "react-router-dom"
import { LayoutDashboard, Monitor, Bell, Shield, LogOut, LogIn, KeyRound, FileCode2, ListChecks, Send, History, Sun, Moon, Users, Boxes, UserCircle, BadgeCheck, LifeBuoy, ArrowRightLeft, Share2, ClipboardCheck } from "lucide-react"
import { logout } from "@/lib/auth"
import { RoutineOpsLogo } from "@/components/RoutineOpsLogo"
import { useMe } from "@/lib/useMe"
import { useCapabilities } from "@/lib/useCapabilities"
import type { Capabilities } from "@/lib/api"
import { useTheme } from "@/lib/theme"
import { cn } from "@/lib/utils"
import api from "@/lib/api"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { toast } from "@/lib/toast"
import { useT } from "@/lib/i18n"
import { LangSwitcher } from "@/components/LangSwitcher"

const M = {
  navOverview: { ru: "Обзор", en: "Overview" },
  navAlerts: { ru: "Алерты", en: "Alerts" },
  navHelp: { ru: "Обращения", en: "Help requests" },
  navAudit: { ru: "Журнал", en: "Audit log" },
  navDevices: { ru: "Устройства", en: "Devices" },
  navEnrollment: { ru: "Энроллмент", en: "Enrollment" },
  navMigration: { ru: "Миграция", en: "Migration" },
  navGroups: { ru: "Группы", en: "Groups" },
  navScripts: { ru: "Скрипты", en: "Scripts" },
  navScriptPolicies: { ru: "Политики скриптов", en: "Script policies" },
  navPolicies: { ru: "Политики", en: "Policies" },
  navAccessRequests: { ru: "Заявки на права", en: "Access requests" },
  navProfile: { ru: "Профиль", en: "Profile" },
  navUsers: { ru: "Пользователи", en: "Users" },
  navLicense: { ru: "Лицензия", en: "License" },
  navSiem: { ru: "SIEM-экспорт", en: "SIEM export" },
  navCompliance: { ru: "Соответствие", en: "Compliance" },
  secHosts: { ru: "Хосты", en: "Hosts" },
  secManagement: { ru: "Управление", en: "Management" },
  secSettings: { ru: "Настройки", en: "Settings" },
  roleAdmin: { ru: "Админ", en: "Admin" },
  roleViewer: { ru: "Наблюдатель", en: "Viewer" },
  themeLight: { ru: "Светлая тема", en: "Light theme" },
  themeDark: { ru: "Тёмная тема", en: "Dark theme" },
  tgConnected: { ru: "Telegram ✓", en: "Telegram ✓" },
  tgConnect: { ru: "Подключить Telegram", en: "Connect Telegram" },
  logout: { ru: "Выход", en: "Log out" },
  tgTitle: { ru: "Telegram уведомления", en: "Telegram notifications" },
  tgConnectedMsg: { ru: "Telegram подключён. Вы получаете уведомления.", en: "Telegram connected. You are receiving notifications." },
  tgConnectMsg: {
    ru: "Подключите Telegram, чтобы получать уведомления об алертах и заявках на права.",
    en: "Connect Telegram to receive notifications about alerts and access requests.",
  },
  tgSendBotPre: { ru: "Отправьте боту", en: "Send the bot" },
  tgSendBotPost: { ru: "команду:", en: "the command:" },
  tgSendBotOrg: { ru: "Отправьте Telegram-боту вашей организации команду:", en: "Send your organization's Telegram bot the command:" },
  tgTokenOnce: { ru: "Токен одноразовый. Если не сработал — сгенерируйте новый.", en: "The token is single-use. If it did not work, generate a new one." },
  tgGenerating: { ru: "Генерация...", en: "Generating..." },
  tgGenerateNew: { ru: "Сгенерировать новый токен", en: "Generate a new token" },
  tgGet: { ru: "Получить токен", en: "Get a token" },
  tgLoadErr: { ru: "Не удалось загрузить статус Telegram", en: "Failed to load Telegram status" },
  tgGenErr: { ru: "Не удалось сгенерировать токен", en: "Failed to generate token" },
}

export default function Layout() {
  const t = useT()
  const navigate = useNavigate()
  const location = useLocation()
  const { theme, toggleTheme } = useTheme()
  const { isAdmin, me } = useMe()
  const { caps } = useCapabilities()
  const [pendingCount, setPendingCount] = useState(0)
  const [queueCount, setQueueCount] = useState(0)
  const [helpCount, setHelpCount] = useState(0)
  const [tgOpen, setTgOpen] = useState(false)
  const [tgLinked, setTgLinked] = useState(false)
  const [tgToken, setTgToken] = useState<string | null>(null)
  const [tgLoading, setTgLoading] = useState(false)
  // Имя бота приходит с сервера (getMe): у каждого деплоя свой бот от @BotFather.
  const [tgBotUsername, setTgBotUsername] = useState("")

  useEffect(() => {
    api.get<{ linked: boolean }>("/profile/telegram")
      .then((r) => setTgLinked(r.data.linked))
      .catch(() => { })
  }, [])

  // Счётчики бейджей — отдельным эффектом, потому что у них другой жизненный цикл:
  // (1) оба admin-only, а без гейта viewer тянул бы ВЕСЬ список устройств ради бейджа,
  //     который ему даже не рисуется;
  // (2) isAdmin приезжает асинхронно из /me, поэтому он в зависимостях — иначе бейджи
  //     навсегда остались бы нулевыми при первом входе;
  // (3) ключ по pathname: считалось один раз за сессию, и после одобрения всей очереди
  //     сайдбар продолжал показывать старое число, споря с пустой таблицей рядом.
  // Отдельной ручки-счётчика на сервере нет — считаем по общему списку.
  // ponytail: клиентский подсчёт, серверный счётчик — когда списки получат пагинацию
  useEffect(() => {
    if (!isAdmin) return
    api.get<{ id: string }[]>("/admin-access-requests?status=pending")
      .then((r) => setPendingCount(r.data?.length ?? 0))
      .catch(() => { })
    api.get<{ status: string }[]>("/devices")
      .then((r) => setQueueCount((r.data ?? []).filter((d) => d.status === "pending_approval").length))
      .catch(() => { })
  }, [isAdmin, location.pathname])

  // Бейдж «Обращений» — без isAdmin-гейта: страница и список доступны и viewer'у
  // (как алерты), а ручка отдаёт только новые, без скриншотов.
  useEffect(() => {
    api.get<{ id: string }[]>("/help-requests?status=new")
      .then((r) => setHelpCount(r.data?.length ?? 0))
      .catch(() => { })
  }, [location.pathname])

  async function openTelegramDialog() {
    setTgOpen(true)
    try {
      const r = await api.get<{ linked: boolean; link_token: string | null; bot_username: string }>("/profile/telegram")
      setTgLinked(r.data.linked)
      setTgToken(r.data.link_token)
      setTgBotUsername(r.data.bot_username ?? "")
    } catch {
      toast({ title: t(M.tgLoadErr), variant: "destructive" })
    }
  }

  async function generateToken() {
    setTgLoading(true)
    try {
      const r = await api.post<{ token: string }>("/profile/telegram-link", {})
      setTgToken(r.data.token)
    } catch {
      toast({ title: t(M.tgGenErr), variant: "destructive" })
    } finally {
      setTgLoading(false)
    }
  }

  async function handleLogout() {
    await logout()
    navigate("/login")
  }

  // adminOnly скрывает пункт для роли viewer (бэкенд всё равно 403'ит мутации — это UX).
  // Иконки монохромные: активный пункт метится фирменным синим (см. .nav-item-active).
  // Группы — плоские подписи, а не сворачиваемые секции: пунктов мало, прятать нечего,
  // а свёрнутая группа спрятала бы счётчики энроллмента и заявок на права.
  const navSections = [
    {
      title: null,
      items: [
        { to: "/", label: t(M.navOverview), icon: LayoutDashboard, badge: 0, adminOnly: false },
        { to: "/alerts", label: t(M.navAlerts), icon: Bell, badge: 0, adminOnly: false },
        { to: "/help-requests", label: t(M.navHelp), icon: LifeBuoy, badge: helpCount, adminOnly: false },
        { to: "/audit-log", label: t(M.navAudit), icon: History, badge: 0, adminOnly: false },
      ],
    },
    {
      title: t(M.secHosts),
      items: [
        { to: "/devices", label: t(M.navDevices), icon: Monitor, badge: 0, adminOnly: false },
        { to: "/enrollment", label: t(M.navEnrollment), icon: LogIn, badge: queueCount, adminOnly: true },
        { to: "/migration", label: t(M.navMigration), icon: ArrowRightLeft, badge: 0, adminOnly: true },
        { to: "/groups", label: t(M.navGroups), icon: Boxes, badge: 0, adminOnly: true },
      ],
    },
    {
      title: t(M.secManagement),
      items: [
        { to: "/scripts", label: t(M.navScripts), icon: FileCode2, badge: 0, adminOnly: true },
        { to: "/script-policies", label: t(M.navScriptPolicies), icon: ListChecks, badge: 0, adminOnly: true },
        { to: "/policies", label: t(M.navPolicies), icon: Shield, badge: 0, adminOnly: true },
        { to: "/admin-access", label: t(M.navAccessRequests), icon: KeyRound, badge: pendingCount, adminOnly: true },
      ],
    },
    {
      title: t(M.secSettings),
      items: [
        { to: "/profile", label: t(M.navProfile), icon: UserCircle, badge: 0, adminOnly: false },
        { to: "/users", label: t(M.navUsers), icon: Users, badge: 0, adminOnly: true },
        { to: "/license", label: t(M.navLicense), icon: BadgeCheck, badge: 0, adminOnly: true },
        // cap: пункт enterprise-фичи — виден, только если лицензия её включает (в open-core
        // /capabilities=404 → caps.siem_export=false → пункт скрыт, а не битый роут).
        { to: "/siem", label: t(M.navSiem), icon: Share2, badge: 0, adminOnly: true, cap: "siem_export" },
        { to: "/compliance", label: t(M.navCompliance), icon: ClipboardCheck, badge: 0, adminOnly: true, cap: "compliance" },
      ],
    },
  ]
    .map((s) => ({
      ...s,
      items: s.items.filter((i) => {
        if (i.adminOnly && !isAdmin) return false
        const cap = (i as { cap?: keyof Capabilities }).cap
        return !cap || caps[cap]
      }),
    }))
    // У viewer «Управление» пустеет целиком — заголовок без пунктов не рисуем.
    .filter((s) => s.items.length > 0)

  return (
    <div className="flex h-screen">
      <aside className="w-[236px] flex-shrink-0 flex flex-col sidebar-glass z-10">
        {/* Плашка логотипа: тёмно-синяя, как круг на знаке. Почта живёт здесь
            (а не внизу списка) — так шапка сайдбара отвечает «кто вошёл». */}
        <div className="h-[72px] flex items-center gap-2.5 px-5 border-b border-[var(--sidebar-border)] bg-[var(--logo-plate)]">
          <NavLink to="/" className="flex items-center gap-2.5 min-w-0 hover:opacity-80 transition-opacity">
            <RoutineOpsLogo size={30} />
            <div className="min-w-0">
              <div className="text-[15px] font-semibold text-foreground leading-tight">RoutineOps</div>
              {me && (
                <div className="text-[11px] text-[var(--logo-plate-fg)] truncate" title={me.email}>
                  {me.email}
                </div>
              )}
            </div>
          </NavLink>
        </div>

        <nav className="flex-1 overflow-y-auto px-2.5 py-3.5 flex flex-col gap-0.5">
          {navSections.map((section, si) => (
          <div key={section.title ?? `plain-${si}`} className={cn("flex flex-col gap-0.5", si > 0 && "mt-3")}>
            {section.title && (
              <div className="px-3 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70">
                {section.title}
              </div>
            )}
            {section.items.map(({ to, label, icon: Icon, badge }) => (
            <NavLink
              key={to}
              to={to}
              end={to === "/"}
              className={({ isActive }) =>
                cn("nav-item", isActive ? "nav-item-active" : "text-muted-foreground")
              }
            >
              {({ isActive }) => (
                <>
                  <Icon className={cn(
                    "h-[17px] w-[17px] flex-shrink-0 transition-colors duration-200",
                    isActive ? "text-brand" : "text-muted-foreground"
                  )} />
                  <span className="flex-1 truncate">{label}</span>
                  {badge > 0 && (
                    // Цифры на градиенте — тёмные по той же причине, что и подпись
                    // primary-кнопки: белые дали бы 2.6:1 на самом мелком тексте оболочки.
                    <span className="ml-auto brand-gradient text-white dark:text-[hsl(224_14%_10%)] text-xs font-semibold rounded-full px-1.5 h-[22px] min-w-[22px] flex items-center justify-center leading-none">
                      {badge}
                    </span>
                  )}
                </>
              )}
            </NavLink>
            ))}
          </div>
          ))}
        </nav>

        <div className="p-2.5 border-t border-[var(--sidebar-border)] flex flex-col gap-0.5">
          {me && (
            <div className="px-3 pb-1 text-[11px] text-muted-foreground truncate">
              {me.role === "it_admin" ? t(M.roleAdmin) : t(M.roleViewer)}
            </div>
          )}
          <div className="flex items-center justify-between gap-2 px-3 py-1">
            <button
              type="button"
              onClick={toggleTheme}
              className="inline-flex items-center gap-2 text-[13px] text-muted-foreground transition-colors hover:text-foreground"
            >
              {theme === "dark"
                ? <Sun className="h-[17px] w-[17px]" />
                : <Moon className="h-[17px] w-[17px]" />}
              {theme === "dark" ? t(M.themeLight) : t(M.themeDark)}
            </button>
            <LangSwitcher />
          </div>
          <button
            type="button"
            onClick={openTelegramDialog}
            className="nav-item text-muted-foreground w-full"
          >
            <Send className="h-[17px] w-[17px]" />
            {tgLinked ? t(M.tgConnected) : t(M.tgConnect)}
          </button>
          <button
            type="button"
            onClick={handleLogout}
            className="nav-item text-muted-foreground hover:!text-destructive w-full"
          >
            <LogOut className="h-[17px] w-[17px]" />
            Выход
          </button>
        </div>
      </aside>

      <Dialog open={tgOpen} onOpenChange={setTgOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(M.tgTitle)}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-1">
            {tgLinked ? (
              <p className="text-sm text-green-700 dark:text-green-400">{t(M.tgConnectedMsg)}</p>
            ) : (
              <p className="text-sm text-muted-foreground">
                {t(M.tgConnectMsg)}
              </p>
            )}
            {tgToken ? (
              <div className="space-y-2">
                <p className="text-sm">
                  {tgBotUsername ? (
                    <>
                      {t(M.tgSendBotPre)}{" "}
                      <a
                        href={`https://t.me/${tgBotUsername}`}
                        target="_blank"
                        rel="noreferrer"
                        className="font-medium underline"
                      >
                        @{tgBotUsername}
                      </a>{" "}
                      {t(M.tgSendBotPost)}
                    </>
                  ) : (
                    <>{t(M.tgSendBotOrg)}</>
                  )}
                </p>
                <code className="block rounded-md border border-border bg-muted px-3 py-2.5 text-sm select-all break-all font-mono">
                  /start {tgToken}
                </code>
                <p className="text-xs text-muted-foreground">{t(M.tgTokenOnce)}</p>
              </div>
            ) : null}
            <Button variant="outline" className="w-full" onClick={generateToken} disabled={tgLoading}>
              {tgLoading ? t(M.tgGenerating) : tgToken ? t(M.tgGenerateNew) : t(M.tgGet)}
            </Button>
          </div>
        </DialogContent>
      </Dialog>

      {/* Верхней градиентной панели нет намеренно (хендофф): первый элемент
          контента — H1 страницы. Колонка ограничена 1180px, чтобы карты не
          растягивались в ленты на широких мониторах. */}
      <main key={location.pathname} className="flex-1 overflow-auto p-6 animate-page-in">
        <div className="max-w-[1180px]">
          <Outlet />
        </div>
      </main>
    </div>
  )
}
