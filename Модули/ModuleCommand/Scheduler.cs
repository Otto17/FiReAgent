// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using Microsoft.Win32.TaskScheduler;
using Newtonsoft.Json;
using System.Threading.Tasks;
using System.IO;
using System.Text;
using System.Security.AccessControl;
using System.Security.Principal;

namespace ModuleCommand
{
    internal class Scheduler
    {
        private const string SCRIPT_NAME = "script"; // Префикс временного файла .bat или .ps1

        // CreateAndRunTaskAsync создает и запускает задачу в Планировщике Windows на основе JSON-параметров
        internal static async Task<string> CreateAndRunTaskAsync(string json)
        {
            string scriptPath = null;                                              // Хранит путь к временному файлу скрипта
            string outputPath = null;                                              // Хранит путь к файлу для захвата вывода
            string taskName = $"FiReMQ_Command_{DateTime.Now:dd.MM.yy(HH.mm.ss)}"; // Формирует уникальное имя задачи в Планировщике

            try
            {
                var parameters = JsonConvert.DeserializeObject<TaskParameters>(json);
                if (parameters == null)
                {
                    Logging.WriteToLogFile("Ошибка: неверные данные в JSON для планировщика.");
                    return "Ошибка: неверные данные в JSON.";
                }

                // Проверяет отсутствие попыток обхода каталогов ("..")
                if (parameters.WorkingFolder != null && parameters.WorkingFolder.IndexOf("..", StringComparison.Ordinal) != -1)
                {
                    Logging.WriteToLogFile("Ошибка: неверный формат пути рабочей папки.");
                    return "Ошибка: неверный формат пути рабочей папки.";
                }

                // Использует стандартную папку "ProgramData\FiReAgent\Command" для лог-файлов
                string outputDir = null;
                if (parameters.CaptureOutput)
                {
                    outputDir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "FiReAgent", "Command");

                    try
                    {
                        Directory.CreateDirectory(outputDir);

                        // Устанавливает права доступа для обеспечения возможности записи под разными учетными записями
                        try
                        {
                            var di = new DirectoryInfo(outputDir);
                            var ds = di.GetAccessControl();

                            var auSid = new SecurityIdentifier("S-1-5-11"); // Прошедшие проверку пользователи
                            ds.AddAccessRule(new FileSystemAccessRule(
                                auSid, FileSystemRights.Modify,
                                InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit,
                                PropagationFlags.None, AccessControlType.Allow));

                            var systemSid = new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null);
                            ds.AddAccessRule(new FileSystemAccessRule(
                                systemSid, FileSystemRights.FullControl,
                                InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit,
                                PropagationFlags.None, AccessControlType.Allow));

                            di.SetAccessControl(ds);
                        }
                        catch (Exception exAcl)
                        {
                            Logging.WriteToLogFile($"ACL warning for '{outputDir}': {exAcl.Message}");
                        }
                    }
                    catch (Exception ex)
                    {
                        Logging.WriteToLogFile($"Ошибка: не удалось создать папку вывода '{outputDir}': {ex.Message}");
                        return $"Ошибка: не удалось создать папку вывода '{outputDir}'";
                    }
                }

                using TaskService ts = new();
                var existingTask = ts.GetTask(taskName);

                if (existingTask != null)
                {
                    // Предотвращает попытку удалить или перезаписать запущенную задачу
                    if (existingTask.State == TaskState.Running)
                        return "Задача ещё выполняется, попробуйте позже...";

                    ts.RootFolder.DeleteTask(taskName, false);
                    Logging.WriteToLogFile("Старая задача удалена.");
                }

                // Определяет полный путь к исполняемому файлу терминала (cmd или powershell)
                string terminalPath;
                bool isCmd = parameters.Terminal.ToLower().Contains("cmd");
                bool isPS = parameters.Terminal.ToLower().Contains("powershell");

                if (isCmd)
                {
                    terminalPath = Environment.ExpandEnvironmentVariables(@"%SystemRoot%\System32\cmd.exe");
                }
                else if (isPS)
                {
                    terminalPath = Environment.ExpandEnvironmentVariables(@"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe");
                }
                else
                {
                    Logging.WriteToLogFile("Ошибка: неизвестный терминал " + parameters.Terminal);
                    return "Ошибка: неизвестный терминал " + parameters.Terminal;
                }

                if (!File.Exists(terminalPath))
                {
                    Logging.WriteToLogFile($"Ошибка: файл {terminalPath} не существует.");
                    return $"Ошибка: файл {terminalPath} не существует.";
                }

                // Определяет подходящую директорию, где создавать скрипт: приоритет — WorkingFolder; иначе outputDir/Temp
                string scriptDir;
                string preferred = string.IsNullOrWhiteSpace(parameters.WorkingFolder) ? null : parameters.WorkingFolder.Trim();
                if (!string.IsNullOrEmpty(preferred))
                {
                    try
                    {
                        preferred = Path.GetFullPath(preferred);

                        // Создаёт папку, если она указана, но не существует
                        if (!Directory.Exists(preferred))
                        {
                            string root = Path.GetPathRoot(preferred);
                            if (!string.Equals(preferred, root, StringComparison.OrdinalIgnoreCase))
                            {
                                Directory.CreateDirectory(preferred);
                            }
                        }

                        if (Directory.Exists(preferred))
                        {
                            scriptDir = preferred;
                        }
                        else
                        {
                            // Возвращается к папке вывода или временной папке, если WorkingFolder не подходит
                            scriptDir = outputDir ?? Path.GetTempPath();
                            Directory.CreateDirectory(scriptDir);
                        }
                    }
                    catch
                    {
                        // Использует папку вывода или временную папку в случае ошибки доступа/формата
                        scriptDir = outputDir ?? Path.GetTempPath();
                        Directory.CreateDirectory(scriptDir);
                    }
                }
                else
                {
                    // Использует папку вывода или временную папку, если WorkingFolder не указан
                    scriptDir = outputDir ?? Path.GetTempPath();
                    Directory.CreateDirectory(scriptDir);
                }

                Logging.WriteToLogFile($"scriptDir='{scriptDir}', workingFolder='{parameters.WorkingFolder}', outputDir='{outputDir}'");

                var extension = isPS ? ".ps1" : ".bat";
                var shortId = Guid.NewGuid().ToString("N").Substring(0, 12); // Берёт первые 12 символов из GUID
                scriptPath = Path.Combine(scriptDir, $"{SCRIPT_NAME}_{shortId}{extension}");

                string scriptText = parameters.Command +
                    (isPS ? "\nexit $LASTEXITCODE" : "\r\nexit /b %errorlevel%");

                // Использует UTF-8 с BOM для PowerShell, чтобы избежать проблем с кодировкой специальных символов
                if (isPS)
                    File.WriteAllText(scriptPath, scriptText, new UTF8Encoding(true)); // true = UTF-8 + BOM
                else
                    // Использует OEM-кодировку (866) для CMD, чтобы корректно отображать русский вывод в консоли
                    File.WriteAllText(scriptPath, scriptText, Encoding.GetEncoding(866));

                Logging.WriteToLogFile($"Создан временный скрипт: {scriptPath}");

                // Определяет путь для файла логирования вывода команды
                if (parameters.CaptureOutput)
                {
                    outputPath = Path.Combine(outputDir, $"{taskName}.log");
                    try { if (File.Exists(outputPath)) File.Delete(outputPath); } catch { /* ok */ }
                }

                // Формирует строку аргументов для передачи терминалу (cmd.exe или powershell.exe)
                string arguments;
                if (isPS)
                {
                    // Экранирует одинарные кавычки для PowerShell-строки
                    string psScript = scriptPath.Replace("'", "''");
                    string psOut = (outputPath ?? string.Empty).Replace("'", "''");

                    if (parameters.CaptureOutput)
                    {
                        // Использует -Command с блоком скрипта для перенаправления stdout/stderr в лог-файл
                        arguments =
                            $"-NoProfile -NonInteractive -ExecutionPolicy Bypass " +
                            $"-Command \"& {{ & '{psScript}' *>&1 | Out-File -FilePath '{psOut}' -Encoding UTF8 -Force; exit $LASTEXITCODE }}\"";
                    }
                    else
                    {
                        arguments = $"-NoProfile -NonInteractive -ExecutionPolicy Bypass -File \"{scriptPath}\"";
                    }
                }
                else
                {
                    // Использует '/c' для запуска скрипта и редиректит вывод, если требуется
                    arguments = parameters.CaptureOutput
                        ? $"/c \"\"{scriptPath}\" > \"{outputPath}\" 2>&1\""
                        : $"/c \"{scriptPath}\"";
                }

                // Рабочая папка задачи — именно WorkingFolder, если он указан и существует, иначе папка скрипта
                string workingDirectory = (!string.IsNullOrWhiteSpace(parameters.WorkingFolder) && Directory.Exists(parameters.WorkingFolder))
                        ? parameters.WorkingFolder
                        : scriptDir;

                var action = new ExecAction(terminalPath, arguments, workingDirectory);

                // Создаёт задачу
                TaskDefinition td = ts.NewTask();

                // Описание задачи
                td.RegistrationInfo.Description = "Выполнение задачи из FiReMQ от модуля 'ModuleCommand'.";

                // Устанавливает версию планировщика от Windows 8 и выше
                td.Settings.Compatibility = TaskCompatibility.V2_2;

                // Настройка выполнения задачи
                if (parameters.RunWhetherUserIsLoggedOnOrNot)
                {
                    // Режим "Выполнять для всех пользователей"
                    if (string.IsNullOrWhiteSpace(parameters.User) || parameters.User.ToUpper() == "СИСТЕМА")
                    {
                        // Если пользователь не указан или "СИСТЕМА" - использует системный аккаунт
                        td.Principal.LogonType = TaskLogonType.ServiceAccount;
                        td.Principal.UserId = "SYSTEM";
                    }
                    else
                    {
                        // Использует указанного пользователя с паролем
                        td.Principal.LogonType = TaskLogonType.Password;
                        td.Principal.UserId = parameters.User;
                    }

                    // Явно устанавливает уровень прав независимо от RunWithHighestPrivileges
                    td.Principal.RunLevel = parameters.RunWithHighestPrivileges ? TaskRunLevel.Highest : TaskRunLevel.LUA;
                }
                else
                {
                    // Режим "Выполнять только для пользователей, вошедших в систему"
                    if (string.IsNullOrWhiteSpace(parameters.User) || parameters.User.ToUpper() == "СИСТЕМА")
                    {
                        // Если пользователь не указан или "СИСТЕМА" - использует системный аккаунт
                        td.Principal.LogonType = TaskLogonType.ServiceAccount;
                        td.Principal.UserId = "SYSTEM";
                    }
                    else
                    {
                        // Использует указанного пользователя
                        td.Principal.LogonType = TaskLogonType.InteractiveToken;
                        td.Principal.UserId = parameters.User;
                    }

                    // Учёт флага наивысших привилегий
                    td.Principal.RunLevel = parameters.RunWithHighestPrivileges ? TaskRunLevel.Highest : TaskRunLevel.LUA;
                }

                // Добавляет действие в коллекцию
                td.Actions.Add(action);

                // Триггер для немедленного запуска
                td.Triggers.Add(new TimeTrigger { StartBoundary = DateTime.Now });

                // Отключает условия, связанные с питанием
                td.Settings.IdleSettings.StopOnIdleEnd = false; // Снимает галочку "Останавливать при выходе компьютера из простоя"
                td.Settings.StopIfGoingOnBatteries = false;     // Снимает галочку "Останавливать, при переходе на питание от батареи"
                td.Settings.DisallowStartIfOnBatteries = false; // Снимает галочку "Запускать только при питании от электросети"
                td.Settings.StartWhenAvailable = true;          // Включает "Немедленно запускать при пропуске"

                // Меняет значение "Останавливать задачу, выполняемую дольше: 3 дн." на 8 часов
                td.Settings.ExecutionTimeLimit = TimeSpan.FromHours(8);

                // Регистрация задачи
                if ((!parameters.RunWhetherUserIsLoggedOnOrNot || !string.IsNullOrWhiteSpace(parameters.User)) &&
                    !string.IsNullOrWhiteSpace(parameters.User) &&
                    parameters.User.ToUpper() != "СИСТЕМА" &&
                    !string.IsNullOrWhiteSpace(parameters.Password))
                {
                    // Регистрирует задачу, требующую ввода пароля
                    ts.RootFolder.RegisterTaskDefinition(
                        taskName, td, TaskCreation.CreateOrUpdate,
                        parameters.User, parameters.Password,
                        parameters.RunWhetherUserIsLoggedOnOrNot ? TaskLogonType.Password : TaskLogonType.InteractiveToken);
                }
                else
                {
                    // Регистрирует задачу, не требующую пароля
                    ts.RootFolder.RegisterTaskDefinition(taskName, td);
                }

                // Немедленный запуск задачи
                var task = ts.GetTask(taskName);
                if (task == null)
                {
                    Logging.WriteToLogFile("Ошибка: задача не создана.");
                    return "Ошибка: задача не создана.";
                }

                try
                {
                    task.Run();
                    Logging.WriteToLogFile("Новая задача создана и запущена.");
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Ошибка запуска задачи: {ex.Message}");
                    return $"Ошибка запуска задачи: {ex.Message}";
                }

                // Отслеживание состояния задачи (проверка каждые 3 секунды)
                TaskState state;
                do
                {
                    await System.Threading.Tasks.Task.Delay(3000);
                    task = ts.GetTask(taskName);

                    if (task == null)
                    {
                        Logging.WriteToLogFile("Задача удалена до завершения.");
                        return "Задача удалена до завершения.";
                    }
                    state = task.State;
                } while (state == TaskState.Running);

                // Удаление задачи
                if (ts.GetTask(taskName) != null) ts.RootFolder.DeleteTask(taskName, false);

                // Чтение файла вывода (если он был создан)
                string outputText = null;
                long outputBytes = 0;

                if (parameters.CaptureOutput && !string.IsNullOrEmpty(outputPath))
                {
                    try
                    {
                        // Использует короткий цикл задержки для ожидания записи данных в файл
                        for (int i = 0; i < 15; i++)
                        {
                            if (File.Exists(outputPath)) break;
                            await System.Threading.Tasks.Task.Delay(200);
                        }

                        if (File.Exists(outputPath))
                        {
                            byte[] bytes = File.ReadAllBytes(outputPath);
                            outputBytes = bytes.LongLength;

                            // Ограничивает размер считываемых данных, чтобы избежать перегрузки памяти
                            int limit = parameters.OutputMaxBytes > 0 ? parameters.OutputMaxBytes : 262144;
                            if (bytes.Length > limit)
                            {
                                // Считывает только "хвост" файла, если размер превышает лимит
                                var tail = new byte[limit];
                                Buffer.BlockCopy(bytes, bytes.Length - limit, tail, 0, limit);
                                bytes = tail;
                            }

                            // Для cmd вывод в OEM-866, для PowerShell в UTF-8
                            Encoding sourceEncoding = isPS
                                ? new UTF8Encoding(false, false)   // PowerShell лог пишется в UTF-8
                                : Encoding.GetEncoding(866);       // CMD лог пишется в OEM866

                            outputText = sourceEncoding.GetString(bytes);

                            // Удаляет Byte Order Mark (BOM), который может присутствовать в выводе PowerShell
                            if (isPS && !string.IsNullOrEmpty(outputText) && outputText[0] == '\uFEFF')
                                outputText = outputText.Substring(1); ;
                        }
                    }
                    catch (Exception ex)
                    {
                        Logging.WriteToLogFile($"Ошибка чтения файла вывода: {ex.Message}");
                    }

                    // Удаляет файл вывода, чтобы не оставлять мусор в системе
                    try { if (File.Exists(outputPath)) File.Delete(outputPath); } catch { /* ok */ }
                }

                var resp = new TaskRunResponse
                {
                    Output = CleanOutput(outputText)
                };

                Logging.WriteToLogFile($"Задача \"{taskName}\" завершена со статусом: {state}, bytes: {outputBytes}");
                return JsonConvert.SerializeObject(resp, new JsonSerializerSettings { NullValueHandling = NullValueHandling.Ignore });
            }
            catch (UnauthorizedAccessException ex)
            {
                Logging.WriteToLogFile($"Ошибка доступа при создании задачи: {ex.Message}");
                return $"Ошибка доступа при создании задачи: {ex.Message}";
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка при создании задачи: {ex.Message}");
                return $"Ошибка при создании задачи: {ex.Message}";
            }
            finally
            {
                // Удаление временного скрипта, независимо от ошибок
                if (scriptPath != null && File.Exists(scriptPath))
                {
                    try
                    {
                        File.Delete(scriptPath);
                        Logging.WriteToLogFile($"Временный скрипт удален: {scriptPath}");
                    }
                    catch (Exception ex)
                    {
                        Logging.WriteToLogFile($"Ошибка удаления скрипта: {ex.Message}");
                    }
                }
            }
        }

        // CleanOutput удаляет служебные строки из вывода команды
        private static string CleanOutput(string text)
        {
            if (string.IsNullOrEmpty(text)) return text;

            var sb = new StringBuilder();
            using (var sr = new StringReader(text))
            {
                string line;
                while ((line = sr.ReadLine()) != null)
                {
                    string trimmed = line.Trim();

                    // Исключает строки с prompt'ом (например "C:\...>exit")
                    if (trimmed.EndsWith(">exit", StringComparison.OrdinalIgnoreCase))
                        continue;

                    // Исключает пустые строки prompt'а "C:\...>" (пустой prompt)
                    if (trimmed.EndsWith(">", StringComparison.Ordinal) && trimmed.Contains(@":\"))
                        continue;

                    sb.AppendLine(line);
                }
            }

            // Удаляет лишние пробелы в конце и добавляет один перевод строки
            return sb.ToString().TrimEnd() + Environment.NewLine;
        }
    }


    // TaskParameters содержит параметры, необходимые для создания и запуска задачи в Планировщике Windows
    internal class TaskParameters
    {
        public string Terminal { get; set; }                    // Тип терминала - cmd или PowerShell
        public string Command { get; set; }                     // Команда с аргументами
        public string WorkingFolder { get; set; }               // Рабочая папка
        public bool RunWhetherUserIsLoggedOnOrNot { get; set; } // Запуск от конкретного пользователя или для всех пользователей
        public string User { get; set; }                        // Имя пользователя
        public string Password { get; set; }                    // Пароль пользователя
        public bool RunWithHighestPrivileges { get; set; }      // Флаг для запуска с наивысшими правами
        public bool CaptureOutput { get; set; }                 // Для сбора выполнения из stdout/stderr
        public string OutputFolder { get; set; }                // Куда писать (опционально); по умолчанию ProgramData\FiReAgent\Command
        public int OutputMaxBytes { get; set; } = 262144;       // Максимум (байт) отдаваемого вывода (по умолчанию 256 КБ, чтобы не утонуть)
    }


    // TaskRunResponse содержит структуру ответа для основного процесса (возвращается в JSON)
    internal class TaskRunResponse
    {
        public string Output { get; set; }
    }
}
