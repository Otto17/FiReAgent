// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

#nullable enable

using System;
using System.Runtime.InteropServices;

namespace ModuleInfo
{
    internal static class ProcessLauncher
    {
        // Константы
        const uint CREATE_UNICODE_ENVIRONMENT = 0x00000400; // Создаёт процесс с окружением Unicode
        const uint TOKEN_QUERY = 0x0008;                    // Указывает право на запрос информации о токене
        const uint TOKEN_DUPLICATE = 0x0002;                // Указывает право на создание дубликата токена
        const uint TOKEN_ASSIGN_PRIMARY = 0x0001;           // Указывает право на назначение первичного токена
        const int SecurityImpersonation = 2;                // Уровень имперсонации
        const int TokenPrimary = 1;                         // Тип токена Primary
        const uint INFINITE = 0xFFFFFFFF;                   // Используется для бесконечного ожидания

        // LaunchProcessInActiveSession запускает процесс в активном сеансе и ожидает его завершения
        internal static bool LaunchProcessInActiveSession(string arguments, string? applicationName)
        {
            // Получает идентификатор сессии консоли, чтобы найти активного пользователя
            uint sessionId = WTSGetActiveConsoleSessionId();
            if (sessionId == 0xFFFFFFFF)    // Указывает, что нет активного пользователя
            {
                Logging.WriteToLogFile("Активный сеанс не найден.");
                return false;
            }

            if (!WTSQueryUserToken(sessionId, out IntPtr userToken))
            {
                int error = Marshal.GetLastWin32Error();
                Logging.WriteToLogFile("WTSQueryUserToken не удался. Ошибка: " + error);
                return false;
            }

            if (!DuplicateTokenEx(
                userToken,
                TOKEN_ASSIGN_PRIMARY | TOKEN_DUPLICATE | TOKEN_QUERY,
                IntPtr.Zero,
                SecurityImpersonation,
                TokenPrimary,
                out IntPtr primaryToken))
            {
                // Используется для получения первичного токена, необходимого для CreateProcessAsUser
                int error = Marshal.GetLastWin32Error();
                Logging.WriteToLogFile("DuplicateTokenEx не удался. Ошибка: " + error);
                CloseHandle(userToken);
                return false;
            }

            if (!CreateEnvironmentBlock(out IntPtr envBlock, primaryToken, false))
            {
                // Создаёт корректный блок окружения, чтобы процесс имел доступ к системным переменным
                Logging.WriteToLogFile("CreateEnvironmentBlock не удался. Ошибка: " + Marshal.GetLastWin32Error());
            }

            // Готовит STARTUPINFO и вызывает CreateProcessAsUser
            STARTUPINFO si = new() { cb = Marshal.SizeOf<STARTUPINFO>(), lpDesktop = "winsta0\\default" }; // Указывает, что процесс должен быть виден на рабочем столе
            string cmdLine = $"\"{applicationName}\" {arguments}";
            bool result = CreateProcessAsUser(
                primaryToken,
                null,
                cmdLine,
                IntPtr.Zero,
                IntPtr.Zero,
                false,
                CREATE_UNICODE_ENVIRONMENT,
                envBlock,
                Environment.CurrentDirectory,
                ref si,
                out PROCESS_INFORMATION pi); ;

            if (!result)
            {
                Logging.WriteToLogFile($"CreateProcessAsUser не удался. Ошибка: {Marshal.GetLastWin32Error()}");
            }
            else
            {
                // Ожидает, пока дочерний процесс не завершит работу
                WaitForSingleObject(pi.hProcess, INFINITE);

                // Закрывает дескрипторы процесса/потока
                CloseHandle(pi.hProcess);
                CloseHandle(pi.hThread);
            }

            // Очищает ресурсы
            if (envBlock != IntPtr.Zero) DestroyEnvironmentBlock(envBlock);
            CloseHandle(userToken);
            CloseHandle(primaryToken);

            return result;
        }


        // --- P/Invoke для работы с процессами и сеансами --- //

        // Перечисляет все мониторы, подключенные к системе
        [DllImport("user32.dll", CharSet = CharSet.Auto)]
         internal static extern bool EnumDisplayDevices(string? lpDevice, uint iDevNum, ref DISPLAY_DEVICE lpDisplayDevice, uint dwFlags);

        // Получает параметры отображения для указанного устройства
        [DllImport("user32.dll")]
        internal static extern bool EnumDisplaySettings(string lpszDeviceName, int iModeNum, ref DEVMODE lpDevMode);

        // Получает идентификатор активного сеанса
        [DllImport("kernel32.dll")]
        private static extern uint WTSGetActiveConsoleSessionId();

        // Получает токен пользователя для указанного сеанса
        [DllImport("Wtsapi32.dll", SetLastError = true)]
        private static extern bool WTSQueryUserToken(uint SessionId, out IntPtr phToken);

        // Дублирует токен доступа
        [DllImport("advapi32.dll", SetLastError = true)]
        private static extern bool DuplicateTokenEx(
            IntPtr hExistingToken,
            uint dwDesiredAccess,
            IntPtr lpTokenAttributes,
            int ImpersonationLevel,
            int TokenType,
            out IntPtr phNewToken);

        // Создает блок окружения для указанного токена
        [DllImport("userenv.dll", SetLastError = true)]
        private static extern bool CreateEnvironmentBlock(
            out IntPtr lpEnvironment,
            IntPtr hToken,
            bool bInherit);

        // Уничтожает блок окружения
        [DllImport("userenv.dll", SetLastError = true)]
        private static extern bool DestroyEnvironmentBlock(IntPtr lpEnvironment);

        // Создает процесс от имени другого пользователя
        [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
        private static extern bool CreateProcessAsUser(
            IntPtr hToken,
            string? lpApplicationName,
            string lpCommandLine,
            IntPtr lpProcessAttributes,
            IntPtr lpThreadAttributes,
            bool bInheritHandles,
            uint dwCreationFlags,
            IntPtr lpEnvironment,
            string lpCurrentDirectory,
            ref STARTUPINFO lpStartupInfo,
            out PROCESS_INFORMATION lpProcessInformation);

        // Закрывает дескриптор объекта
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern bool CloseHandle(IntPtr hObject);

        // Ожидает завершение объекта процесса в течение заданного времени
        [DllImport("kernel32.dll", SetLastError = true)]
        private static extern uint WaitForSingleObject(IntPtr hHandle, uint dwMilliseconds);


        // --- Структуры --- //

        // DISPLAY_DEVICE хранит информацию о дисплейном устройствее
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Auto)]
        internal struct DISPLAY_DEVICE
        {
            internal int cb;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            internal string DeviceName;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            internal string DeviceString;
            internal int StateFlags;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            internal string DeviceID;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)]
            internal string DeviceKey;

        }

        // DEVMODE хранит информацию о режиме дисплея
        [StructLayout(LayoutKind.Sequential)]
        internal struct DEVMODE
        {
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            internal string dmDeviceName;
            internal short dmSpecVersion;
            internal short dmDriverVersion;
            internal short dmSize;
            internal short dmDriverExtra;
            internal int dmFields;
            internal int dmPositionX;
            internal int dmPositionY;
            internal int dmDisplayOrientation;
            internal int dmDisplayFixedOutput;
            internal short dmColor;
            internal short dmDuplex;
            internal short dmYResolution;
            internal short dmTTOption;
            internal short dmCollate;
            [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 32)]
            internal string dmFormName;
            internal short dmLogPixels;
            internal int dmBitsPerPel;
            internal int dmPelsWidth;
            internal int dmPelsHeight;
            internal int dmDisplayFlags;
            internal int dmDisplayFrequency;
        }

        // STARTUPINFO хранит информацию о стартовых параметрах процесса
        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        internal struct STARTUPINFO
        {
            internal int cb;
            internal string? lpReserved;
            internal string? lpDesktop;
            internal string? lpTitle;
            internal uint dwX;
            internal uint dwY;
            internal uint dwXSize;
            internal uint dwYSize;
            internal uint dwXCountChars;
            internal uint dwYCountChars;
            internal uint dwFillAttribute;
            internal uint dwFlags;
            internal short wShowWindow;
            internal short cbReserved2;
            internal IntPtr lpReserved2;
            internal IntPtr hStdInput;
            internal IntPtr hStdOutput;
            internal IntPtr hStdError;
        }

        // PROCESS_INFORMATION хранит информацию о процессе
        [StructLayout(LayoutKind.Sequential)]
        internal struct PROCESS_INFORMATION
        {
            internal IntPtr hProcess;
            internal IntPtr hThread;
            internal uint dwProcessId;
            internal uint dwThreadId;
        }
    }
}
