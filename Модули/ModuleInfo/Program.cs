// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.Diagnostics;

namespace ModuleInfo
{
    internal class Program
    {
        private const string CurrentVersion = "17.10.25"; // Текущая версия ModuleInfo в формате "дд.мм.гг"

        // Константа для запуска от пользователя СИСТЕМА и создания дочернего процесса в активном сеансе
        const string ChildArgument = "child";

        // --- Main ---
        internal static void Main(string[] args)
        {
            // Показывает версию ModuleInfo
            if (args.Length >= 1 && string.Equals(args[0], "--version", StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine($"Версия \"ModuleInfo\": {CurrentVersion}");
                return;
            }

            try
            {
                // Проверка обязательных аргументов
                if (args.Length == 0)
                {
                    Console.WriteLine("Не указан режим работы. Используйте ключи 'Lite' или 'Aida'.");
                    return;
                }

                // Запускает LiteInformation напрямую, если если процесс является дочерним (Lite)
                if (args[0].Equals(ChildArgument, StringComparison.OrdinalIgnoreCase))
                {
                    LiteInformation.RunAllInfo();
                    return;
                }

                var mode = args[0];

                if (mode.Equals("Aida", StringComparison.OrdinalIgnoreCase))
                {
                    // Aida64 всегда запускается напрямую (без активного сеанса)
                    AIDA64.Run();
                }
                else if (mode.Equals("Lite", StringComparison.OrdinalIgnoreCase))
                {
                    if (Environment.UserInteractive)
                    {
                        // Запуск в интерактивном режиме GUI
                        LiteInformation.RunAllInfo();
                    }
                    else
                    {
                        // Запуск от SYSTEM: создание дочернего процесса в активном сеансе
                        if (!ProcessLauncher.LaunchProcessInActiveSession(ChildArgument,
                            Process.GetCurrentProcess().MainModule.FileName))
                        {
                            Logging.WriteToLogFile("Не удалось запустить процесс в активном сеансе.");
                        }
                    }
                }
                else
                {
                    Console.WriteLine("Неверный режим работы. Используйте Lite или Aida.");
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка: " + ex.Message);
            }
        }
    }
}
