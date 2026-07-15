@echo off
setlocal EnableDelayedExpansion
chcp 65001 >nul
REM ============================================================================
REM  uninstall.bat — снятие RoutineOps-агента с защитой от удаления (tamper-protection).
REM
REM  Полная процедура (обычный пользователь снять агента НЕ может — нужен админ):
REM    1. Загрузиться в БЕЗОПАСНОМ режиме Windows
REM       (msconfig -> Загрузка -> Безопасный режим, либо Shift+Перезагрузка ->
REM        Поиск и устранение неисправностей -> Параметры загрузки -> 4).
REM    2. Разоружить защиту:
REM         "C:\Program Files\RoutineOps\RoutineOps-agent.exe" tamper-disarm
REM       (либо вручную: HKLM\SOFTWARE\RoutineOps\Agent ->
REM        TamperProtection=0 и SafeBootGuard=0).
REM    3. Перезагрузиться в ОБЫЧНЫЙ режим.
REM    4. Запустить ЭТОТ батник от имени администратора.
REM ============================================================================

REM --- права администратора ---
net session >nul 2>&1
if %errorlevel% neq 0 (
  echo [ОШИБКА] Запустите этот файл от имени администратора.
  pause
  exit /b 1
)

set "EXE=%~dp0RoutineOps-agent.exe"
if not exist "%EXE%" set "EXE=%ProgramFiles%\RoutineOps\RoutineOps-agent.exe"

REM --- проверка, что защита разоружена (по умолчанию считаем снятой, если значения нет) ---
set "PROT=0x0"
set "GUARD=0x0"
for /f "tokens=3" %%a in ('reg query "HKLM\SOFTWARE\RoutineOps\Agent" /v TamperProtection 2^>nul ^| find "TamperProtection"') do set "PROT=%%a"
for /f "tokens=3" %%a in ('reg query "HKLM\SOFTWARE\RoutineOps\Agent" /v SafeBootGuard 2^>nul ^| find "SafeBootGuard"') do set "GUARD=%%a"

if /i not "%PROT%"=="0x0" goto :armed
if /i not "%GUARD%"=="0x0" goto :armed
goto :remove

:armed
echo [СТОП] Tamper-protection ВЗВЕДЕНА (TamperProtection=%PROT% SafeBootGuard=%GUARD%).
echo.
echo Сначала разоружите её в БЕЗОПАСНОМ режиме Windows:
echo     "%EXE%" tamper-disarm
echo затем перезагрузитесь в обычный режим и запустите этот батник снова.
pause
exit /b 2

:remove
echo Останавливаю службу RoutineOps-агента...
sc stop RoutineOps-agent >nul 2>&1
REM дать службе закрыться и освободить exe
timeout /t 3 /nobreak >nul

echo Снимаю SafeBoot-регистрацию и флаги защиты...
if exist "%EXE%" "%EXE%" tamper-cleanup

echo Удаляю службу...
sc delete RoutineOps-agent >nul 2>&1

REM подчистить ключи на случай, если exe уже удалён или tamper-cleanup не отработал
reg delete "HKLM\SYSTEM\CurrentControlSet\Control\SafeBoot\Minimal\RoutineOps-agent" /f >nul 2>&1
reg delete "HKLM\SYSTEM\CurrentControlSet\Control\SafeBoot\Network\RoutineOps-agent" /f >nul 2>&1
reg delete "HKLM\SOFTWARE\RoutineOps" /f >nul 2>&1
reg delete "HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run" /v RoutineOpsTray /f >nul 2>&1

echo Удаляю файлы...
rmdir /s /q "%ProgramFiles%\RoutineOps" >nul 2>&1
rmdir /s /q "%ProgramData%\RoutineOps" >nul 2>&1
REM Следы прежних ручных установок (распаковка MSI вручную ставила службу из этого каталога).
rmdir /s /q "C:\mdm-extract" >nul 2>&1

echo.
echo Готово. RoutineOps-агент удалён.
pause
exit /b 0
