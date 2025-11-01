// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.RegularExpressions;

namespace ModuleInfo
{
    internal class AIDA64
    {
        private const string ConfigDir = "config";          // Имя папки для конфигурации
        private const string ConfigFile = "Aida64.conf";    // Имя файла конфигурации
        private const string KeyPath = "path_Aida64=";      // Ключ для пути к исполняемому файлу AIDA64

        // Run запускает процесс AIDA64, генерирует отчёт и обрабатывает результат
        internal static void Run()
        {
            try
            {
                var baseDir = AppDomain.CurrentDomain.BaseDirectory;
                var configPath = Path.Combine(baseDir, ConfigDir, ConfigFile);

                // Проверяет наличие конфига с краткой справкой
                if (!File.Exists(configPath))
                {
                    Directory.CreateDirectory(Path.GetDirectoryName(configPath));

                    // Собирает шаблонную строку
                    var template = KeyPath + Environment.NewLine + Environment.NewLine + Environment.NewLine +

                     "/*" + Environment.NewLine +
                    "\tОПИСАНИЕ ВСТРОЕННЫХ КЛЮЧЕЙ AIDA64 В МОДУЛЕ \"ModuleInfo.exe\"" + Environment.NewLine + Environment.NewLine +

                    "/R [полный путь к папке создаваемого отчёта]" + Environment.NewLine +
                    "Вычисляется относительно \"ModuleInfo.exe\" до папки \"Reports\" с полным названием файла из имени хоста." + Environment.NewLine + Environment.NewLine +

                    "/MHTML [отчёты будут создаваться в формате .htm (с иконками изображений)]" + Environment.NewLine +
                    "Этот ключ жёстко задан в коде." + Environment.NewLine + Environment.NewLine +

                    "/LANGxx - где «xx» использует двухбуквенный код языка. Выбранный язык должен присутствовать в папке \"Language\" программы AIDA64." + Environment.NewLine +
                    "Этот ключ жёстко задан на Русский язык в коде как \"/LANGru\"." + Environment.NewLine + Environment.NewLine +

                    "/IDLE - устанавливает для процесса приложения AIDA64 незанятый (самый низкий) приоритет." + Environment.NewLine +
                    "Этот ключ жёстко задан в коде." + Environment.NewLine + Environment.NewLine +

                    "/SILENT - скрывает значок AIDA64 в панели задач и всплывающие уведомления." + Environment.NewLine +
                    "Этот ключ жёстко задан в коде." + Environment.NewLine + Environment.NewLine +

                    "/NOLICENSE - отключает и скрывает всю информацию, связанную с лицензиями на программное обеспечение AIDA64." + Environment.NewLine +
                    "Этот ключ жёстко задан в коде." + Environment.NewLine + Environment.NewLine +

                    "/CUSTOM [полный путь к профилю отчёта, из которого будут использоваться указанные пункты]" + Environment.NewLine +
                    "Вычисляется относительно \"ModuleInfo.exe\" до папки \"config\", в которой при отсутствии профиля создаётся файл профиля \"Профиль_Aida64.rpf\" с шаблоном по умолчанию." + Environment.NewLine + Environment.NewLine +

                    "Пример указания полного пути до исполняемого файла AIDA64:" + Environment.NewLine +
                    "# _path_Aida64=\"D:\\AIDA64\\aida64.exe\"" + Environment.NewLine + Environment.NewLine +
                    "*/";

                    File.WriteAllText(configPath, template);

                    Logging.WriteToLogFile("Создан шаблон 'Aida64.conf' в папке 'config'. Заполните путь к AIDA64!");
                    return;
                }

                // Чтение всех строк конфигурации для поиска пути
                var lines = File.ReadAllLines(configPath);
                string path = null;
                foreach (var line in lines)
                {
                    if (line.StartsWith(KeyPath, StringComparison.OrdinalIgnoreCase))
                    {
                        path = line.Substring(KeyPath.Length).Trim().Trim('"');
                        break;
                    }
                }

                // Проверка заполненности конфига
                if (string.IsNullOrEmpty(path))
                {
                    Logging.WriteToLogFile("Заполните путь к исполняемому файлу AIDA64 в конфиге 'Aida64.conf'!");
                    return;
                }

                // Проверяет существование файла уже по «чистому» пути
                if (!File.Exists(path))
                {
                    Logging.WriteToLogFile($"Не найден исполняемый файл Aida64 по пути: {path}");
                    return;
                }

                // Создание папка Reports при её отсутствии
                var reportsDir = Path.Combine(baseDir, "Reports");
                if (!Directory.Exists(reportsDir))
                {
                    Directory.CreateDirectory(reportsDir);
                }

                // Формирует имя файла с префиксом и именем хоста
                string fileName = $"Aida_{Environment.MachineName}.htm";
                var reportFilePath = Path.Combine(reportsDir, fileName);

                // Папка config и профиль
                var configDir = Path.Combine(baseDir, "config");
                if (!Directory.Exists(configDir))
                {
                    Directory.CreateDirectory(configDir);
                }

                // Создание шаблона профиля по умолчанию при его отсутствии
                var profilePath = Path.Combine(configDir, "Профиль_Aida64.rpf");
                if (!File.Exists(profilePath))
                {
                    var profileLines = new[]
                    {
                        "InfoPage=\"Computer;Sensor\"",
                        "InfoPage=\"Motherboard;CPU\"",
                        "InfoPage=\"Motherboard;Motherboard\"",
                        "InfoPage=\"Motherboard;SPD\"",
                        "InfoPage=\"Motherboard;BIOS\"",
                        "InfoPage=\"Operating System;Operating System\"",
                        "InfoPage=\"Operating System;UpTime\"",
                        "InfoPage=\"Server;Share\"",
                        "InfoPage=\"Display;Windows Video\"",
                        "InfoPage=\"Display;GPU\"",
                        "InfoPage=\"Display;Monitor\"",
                        "InfoPage=\"Storage;Logical Drives\"",
                        "InfoPage=\"Storage;Physical Drives\"",
                        "InfoPage=\"Storage;Optical Drives\"",
                        "InfoPage=\"Storage;ATA\"",
                        "InfoPage=\"Storage;SMART\"",
                        "InfoPage=\"Network;Windows Network\"",
                        "InfoPage=\"Devices;Input\"",
                        "InfoPage=\"Devices;Printers\""
                    };

                    File.WriteAllLines(profilePath, profileLines);
                }

                // Аргументы для AIDA64 с полным именем файла
                var args = $"/R \"{reportFilePath}\" /MHTML /LANGru /IDLE /SILENT /NOLICENSE /CUSTOM \"{profilePath}\"";

                // Запуск Aida64 с указанными аргументами
                var psi = new ProcessStartInfo
                {
                    FileName = path,
                    Arguments = args,
                    UseShellExecute = true,
                    Verb = "runas",
                    WorkingDirectory = Path.GetDirectoryName(path)
                };

                // Запускает и ожидает завершения
                using var process = Process.Start(psi);
                process.WaitForExit();

                // Формирует дату и время создания отчёта
                var reportTime = DateTime.Now.ToString("dd.MM.yyyy в HH:mm:ss");

                // Ждёт 1 секунду для завершения создания файла отчёта
                System.Threading.Thread.Sleep(1000);

                // Переименовывает .htm в .html
                string newFilePath = Path.ChangeExtension(reportFilePath, ".html");
                if (!File.Exists(reportFilePath))
                {
                    Logging.WriteToLogFile($"Файл отчёта {reportFilePath} не найден.");
                    return;
                }

                try
                {
                    File.Move(reportFilePath, newFilePath);
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Ошибка при переименовании файла: {ex.Message}");
                    return;
                }

                // Читает содержимое файла с кодировкой Windows-1251
                string content;
                try
                {
                    content = File.ReadAllText(newFilePath, Encoding.GetEncoding(1251));
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Ошибка чтения файла: {ex.Message}");
                    return;
                }

                // Добавляет указание языка в тег <html>
                content = Regex.Replace(
                    content,
                    @"<html\b",
                    "<html lang=\"ru\"",
                    RegexOptions.IgnoreCase
                );

                // Заменяет строку с кодировкой в мета-теге
                content = content.Replace(
                    "<META HTTP-EQUIV=\"Content-Type\" CONTENT=\"text/html; CHARSET=Windows-1251\">",
                    "<meta charset=\"UTF-8\">"
                );

                // Удаляет "right: 20%;" и лишний перенос строки из тега <STYLE>, что бы сделать выравнивание меню слева, а не справа
                content = Regex.Replace(content,
                    @"right: 20%;\r?\n",  // Удаляет строку и следующий за ней перенос строки
                    string.Empty,
                    RegexOptions.IgnoreCase
                );

                // Оставляет в теге <TITLE> только имя хоста
                content = Regex.Replace(content,
                    @"<TITLE>Отчёт: &lt;(.*?)&gt;</TITLE>",
                    "<TITLE>$1</TITLE>",
                    RegexOptions.IgnoreCase
                );

                // Добавляет после <BODY> заголовок с датой и временем создания отчёта
                var h1Tag = $"<h1 style=\"color: #2c3e50; font-family: Arial, sans-serif;\" align=\"center\">Отчёт создан {reportTime}</h1>";
                content = Regex.Replace(content,
                    @"(<BODY[^>]*>)",
                    "$1" + h1Tag,
                    RegexOptions.IgnoreCase
                );

                // Сохраняет изменения в UTF-8 без BOM
                var utf8WithoutBom = new UTF8Encoding(false);

                try
                {
                    File.WriteAllText(newFilePath, content, utf8WithoutBom);
                }
                catch (Exception ex)
                {
                    Logging.WriteToLogFile($"Ошибка сохранения файла: {ex.Message}");
                    return;
                }

                // Сжимает файл
                CompressXZ.Compress_XZ(newFilePath);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка при запуске Aida64: " + ex.Message);
            }
        }
    }
}