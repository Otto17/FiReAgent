// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

#nullable enable

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Management;
using System.Net;
using System.Runtime.InteropServices;
using System.Text;
using Microsoft.Win32;
using System.Diagnostics;
using LibreHardwareMonitor.Hardware;

namespace ModuleInfo
{
    // LiteInformation предоставляет методы для кастомного сбора информации о системе и генерации HTML-отчета
    internal class LiteInformation
    {
        // Константа для проверки, подключено ли устройство к рабочему столу (активно ли оно)
        const int DISPLAY_DEVICE_ATTACHED_TO_DESKTOP = 0x00000001;

        // Буфер для сборки HTML-контента
        private static readonly StringBuilder htmlContent = new();

        // LiteInformation инициализирует базовую структуру HTML и стили
        static LiteInformation()
        {
            htmlContent.AppendLine("<!DOCTYPE html>");
            htmlContent.AppendLine("<html lang=\"ru\">");
            htmlContent.AppendLine("<head>");
            htmlContent.AppendLine("<meta charset=\"UTF-8\">");
            htmlContent.AppendLine("<title>{MACHINE_NAME}</title>");
            htmlContent.AppendLine("<style>");
            htmlContent.AppendLine("body { font-family: Arial, sans-serif; margin: 40px; background-color: #f4f4f9; color: #333; }");
            htmlContent.AppendLine("h1 { color: #2c3e50; text-align: center; }");
            htmlContent.AppendLine("h2 { color: #2980b9; border-bottom: 2px solid #3498db; padding-bottom: 5px; }");
            htmlContent.AppendLine("p { margin: 5px 0; line-height: 1.6; }");
            htmlContent.AppendLine(".section { margin-bottom: 20px; padding: 15px; background-color: #fff; border-radius: 8px; box-shadow: 0 2px 4px rgba(0,0,0,0.1); }");
            htmlContent.AppendLine(".monitor { margin-left: 20px; }");
            htmlContent.AppendLine(".disk, .ram, .logical-disk, .ip { margin-left: 20px; }");
            htmlContent.AppendLine(".highlight-red { color: red; font-weight: bold;}");
            htmlContent.AppendLine("</style>");
            htmlContent.AppendLine("</head>");
            htmlContent.AppendLine("<body>");
            htmlContent.AppendLine("<h1>Отчёт создан {TIME}</h1>");
        }

        // WriteInfo сохраняет форматированные данные в HTML-контент
        private static void WriteInfo(string message, bool isHeader = false, bool isSubItem = false, string subItemClass = "")
        {
            if (isHeader)
            {
                htmlContent.AppendLine($"<div class='section'><h2>{message}</h2>");
            }
            else if (isSubItem)
            {
                htmlContent.AppendLine($"<p class='{subItemClass}'>{message}</p>");
            }
            else
            {
                htmlContent.AppendLine("</div>");
            }
        }

        // RunAllInfo запускает сбор системной информации и сохраняет финальный отчет в формате HTML
        internal static void RunAllInfo()
        {
            OSInfo();
            SharedResources();
            BaseBoardInfo();
            ProcessorInfo();
            VideoControllerInfo();
            MonitorsInfo();
            RAMInfo();
            DiskDrivesInfo();
            LogicalDisksInfo();
            IPAddresses();

            // Закрывает HTML
            htmlContent.AppendLine("</body>");
            htmlContent.AppendLine("</html>");

            // Получает имя машины и формирует имя файла
            string machineName = Environment.MachineName;
            var baseDir = AppDomain.CurrentDomain.BaseDirectory;
            var reportsDir = Path.Combine(baseDir, "Reports");

            // Создаёт папку Reports, если ещё не существует
            if (!Directory.Exists(reportsDir))
            {
                Directory.CreateDirectory(reportsDir);
            }

            // Формирует полный путь к файлу отчёта
            string fileName = Path.Combine(reportsDir, $"Lite_{machineName}.html");

            // Получает текущее время
            string reportTime = DateTime.Now.ToString("dd.MM.yyyy в HH:mm:ss");

            // Заменяет placeholders на актуальные значения
            string finalHtml = htmlContent.ToString().Replace("{MACHINE_NAME}", machineName).Replace("{TIME}", reportTime);

            // Записывает весь собранный HTML-контент в файл (UTF-8 без BOM) с новым именем
            var utf8WithoutBom = new UTF8Encoding(false);
            try
            {
                File.WriteAllText(fileName, finalHtml, utf8WithoutBom);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка сохранения файла: {ex.Message}");
                return;
            }

            // Ждёт 1 секунду для завершения создания файла отчёта
            System.Threading.Thread.Sleep(1000);

            // Сжимает файл после его создания
            CompressXZ.Compress_XZ(fileName);
        }

        // MonitorsInfo собирает и записывает данные о подключенных мониторах
        private static void MonitorsInfo()
        {
            try
            {
                WriteInfo("Монитор:", true);
                List<string> monitors = GetMonitorsInfo();
                if (monitors.Count == 0)
                {
                    WriteInfo("#1: Неизвестный монитор, тип монитора: N/A, разрешение: N/A, частота: N/A, дата выпуска: N/A", isSubItem: true, subItemClass: "monitor");
                }
                else
                {
                    for (int i = 0; i < monitors.Count; i++)
                    {
                        WriteInfo($"#{i + 1}: {monitors[i]}", isSubItem: true, subItemClass: "monitor");
                    }
                }
                WriteInfo("", isHeader: false);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка получения информации о мониторах: {ex.Message}");
            }
        }

        // GetMonitorsInfo получает подробную информацию о мониторах, используя WMI и WinAPI
        private static List<string> GetMonitorsInfo()
        {
            List<ManagementObject> monitorIDs = [];
            try
            {
                var idSearcher = new ManagementObjectSearcher(@"root\wmi", "SELECT * FROM WmiMonitorID");

                // Получает DeviceID и UserFriendlyName для сопоставления с WinAPI
                monitorIDs = [.. idSearcher.Get().Cast<ManagementObject>()];
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка при получении WmiMonitorID: {ex.Message}");
            }

            List<ManagementObject> basicParams;
            try
            {
                var basicSearcher = new ManagementObjectSearcher(@"root\wmi", "SELECT * FROM WmiMonitorBasicDisplayParams");
                
                // Получает физические размеры (BasicDisplayParams) для расчета диагонали
                basicParams = [.. basicSearcher.Get().Cast<ManagementObject>()];
            }
            catch
            {
                basicParams = [];
            }

            List<string> monitors = [];
            int monitorIndex = 0;
            ProcessLauncher.DISPLAY_DEVICE dd = new() { cb = Marshal.SizeOf(typeof(ProcessLauncher.DISPLAY_DEVICE)) };

            for (uint i = 0; ProcessLauncher.EnumDisplayDevices(null, i, ref dd, 0); i++)
            {
                // Обрабатывает только активные мониторы, подключенные к рабочему столу
                if ((dd.StateFlags & DISPLAY_DEVICE_ATTACHED_TO_DESKTOP) != 0)
                {
                    ProcessLauncher.DEVMODE dm = new() { dmSize = (short)Marshal.SizeOf(typeof(ProcessLauncher.DEVMODE)) };
                    string resolution = "N/A";
                    string refreshRate = "N/A";

                    // Получает текущее разрешение и частоту обновления через EnumDisplaySettings
                    if (ProcessLauncher.EnumDisplaySettings(dd.DeviceName, -1, ref dm))
                    {
                        resolution = $"{dm.dmPelsWidth} x {dm.dmPelsHeight}";
                        refreshRate = dm.dmDisplayFrequency + " Гц";
                    }
                    else
                    {
                        Logging.WriteToLogFile($"EnumDisplaySettings не вернул данные для {dd.DeviceName}.");
                    }

                    string monitorName = dd.DeviceString.Trim();
                    if (monitorIndex < monitorIDs.Count)
                    {
                        // Использует EDID (ManufacturerName + UserFriendlyName) для определения полного имени, если доступно
                        string manufacturer = DecodeEdid(monitorIDs[monitorIndex]["ManufacturerName"] as ushort[]);
                        string product = DecodeEdid(monitorIDs[monitorIndex]["UserFriendlyName"] as ushort[]);
                        if (manufacturer != "N/A" && product != "N/A")
                        {
                            monitorName = manufacturer + " " + product;
                        }
                    }

                    string? monitorType = "N/A";
                    bool sizeObtained = false;
                    if (basicParams.Count > monitorIndex)
                    {
                        try
                        {
                            int horizontalSize = Convert.ToInt32(basicParams[monitorIndex]["MaxHorizontalImageSize"]);
                            int verticalSize = Convert.ToInt32(basicParams[monitorIndex]["MaxVerticalImageSize"]);

                            // Рассчитывает диагональ в дюймах, используя максимальный горизонтальный и вертикальный размер из WMI
                            if (horizontalSize > 0 && verticalSize > 0)
                            {
                                double diagonalInches = Math.Sqrt(horizontalSize * horizontalSize + verticalSize * verticalSize) / 2.54;
                                int diagRounded = (int)Math.Round(diagonalInches);
                                monitorType = $"{diagRounded}\" LCD";
                                if (dm.dmPelsWidth == 1280 && dm.dmPelsHeight == 1024)
                                {
                                    monitorType += " (SXGA)";
                                }
                                sizeObtained = true;
                            }
                        }
                        catch { }
                    }

                    // Если WMI не предоставил размер, пытается извлечь его из EDID в реестре
                    if (!sizeObtained && monitorIndex < monitorIDs.Count)
                    {
                        string instanceName = monitorIDs[monitorIndex]["InstanceName"]?.ToString() ?? "";
                        monitorType = GetMonitorPhysicalSizeFromRegistry(instanceName);
                    }

                    string releaseDate = "N/A";
                    if (monitorIndex < monitorIDs.Count)
                    {
                        try
                        {
                            var weekObj = monitorIDs[monitorIndex]["WeekOfManufacture"];
                            var yearObj = monitorIDs[monitorIndex]["YearOfManufacture"];
                            if (weekObj != null && yearObj != null)
                            {
                                int week = Convert.ToInt32(weekObj);
                                int year = Convert.ToInt32(yearObj);
                                if (week > 0 && year > 0)
                                {
                                    releaseDate = $"Неделя {week} / Год {year}";
                                }
                            }
                        }
                        catch
                        {
                            releaseDate = "N/A";
                        }
                    }

                    string monitorInfo = $"{monitorName}, тип монитора: {monitorType}, разрешение: {resolution}, частота: {refreshRate}, дата выпуска: {releaseDate}";
                    monitors.Add(monitorInfo);
                    monitorIndex++;
                }
                dd.cb = Marshal.SizeOf(typeof(ProcessLauncher.DISPLAY_DEVICE));
            }
            return monitors;
        }

        // GetMonitorPhysicalSizeFromRegistry извлекает физический размер монитора из EDID в реестре
        private static string? GetMonitorPhysicalSizeFromRegistry(string instanceName)
        {
            try
            {
                string[] parts = instanceName.Split('\\');
                if (parts.Length < 3)
                    return "N/A";

                // Формирует путь к ключу Device Parameters на основе InstanceName WMI
                string keyPath = $@"SYSTEM\CurrentControlSet\Enum\DISPLAY\{parts[1]}\{parts[2]}\Device Parameters";
                using RegistryKey regKey = Registry.LocalMachine.OpenSubKey(keyPath);
                if (regKey != null)
                {
                    if (regKey.GetValue("EDID") is byte[] edid && edid.Length >= 23)
                    {
                        int horizontalCm = edid[21];
                        int verticalCm = edid[22];

                        // Байт 21 и 22 содержат горизонтальный и вертикальный размер в сантиметрах
                        if (horizontalCm > 0 && verticalCm > 0)
                        {
                            double diagonalInches = Math.Sqrt(horizontalCm * horizontalCm + verticalCm * verticalCm) / 2.54;
                            int diagRounded = (int)Math.Round(diagonalInches);
                            return $"{diagRounded}\" LCD";
                        }
                    }
                }
            }
            catch { }
            return "N/A";
        }

        // DecodeEdid преобразует массив ushort, содержащий EDID-строку, в читаемый текст
        private static string DecodeEdid(ushort[]? data)
        {
            if (data == null)
                return "N/A";
            StringBuilder result = new();
            foreach (ushort c in data)
            {
                if (c == 0)
                    break;
                result.Append((char)c);
            }
            return string.IsNullOrWhiteSpace(result.ToString()) ? "N/A" : result.ToString();
        }

        // GetWorkgroup получает название рабочей группы компьютера
        private static string GetWorkgroup()
        {
            try
            {
                var searcher = new ManagementObjectSearcher("SELECT Workgroup FROM Win32_ComputerSystem");
                foreach (ManagementObject cs in searcher.Get().Cast<ManagementObject>())
                {
                    return cs["Workgroup"]?.ToString() ?? "N/A";
                }
            }
            catch { }
            return "N/A";
        }

        // GetProductKey извлекает и декодирует лицензионный ключ Windows (DPID) из реестра
        private static string GetProductKey()
        {
            try
            {
                byte[] digitalProductId = (byte[])Registry.GetValue(@"HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows NT\CurrentVersion", "DigitalProductId", null);
                if (digitalProductId == null)
                    return "N/A";

                const string KeyChars = "BCDFGHJKMPQRTVWXY2346789";
                string key = "";
                byte[] keyBytes = new byte[15];
                Array.Copy(digitalProductId, 52, keyBytes, 0, 15);

                // Стандартный алгоритм дешифрования DigitalProductId (DPID) для получения ключа
                for (int i = 24; i >= 0; i--)
                {
                    int k = 0;
                    for (int j = 14; j >= 0; j--)
                    {
                        k = (k * 256) ^ keyBytes[j];
                        keyBytes[j] = (byte)(k / 24);
                        k %= 24;
                    }
                    key = KeyChars[k] + key;
                }
                for (int i = 5; i < key.Length; i += 6)
                    key = key.Insert(i, "-");
                return key;
            }
            catch
            {
                return "N/A";
            }
        }

        // OSInfo собирает и записывает основную информацию об операционной системе
        private static void OSInfo()
        {
            try
            {
                // Извлечение версии ОС из реестра, так как WMI может давать неполную информацию
                var key = Registry.LocalMachine.OpenSubKey(@"SOFTWARE\Microsoft\Windows NT\CurrentVersion");
                string? displayVersion = key.GetValue("DisplayVersion", null) as string;
                string currentVersion = key.GetValue("CurrentVersion", "N/A").ToString();
                string ver = displayVersion ?? currentVersion;
                string build = key.GetValue("CurrentBuildNumber", "N/A").ToString();
                string ubr = (key.GetValue("UBR", 0)?.ToString() ?? "0");

                var searcher = new ManagementObjectSearcher("SELECT * FROM Win32_OperatingSystem");
                foreach (ManagementObject os in searcher.Get().Cast<ManagementObject>())
                {
                    WriteInfo("Компьютер:", true);
                    WriteInfo("Имя машины: " + Environment.MachineName, isSubItem: true);
                    WriteInfo("Рабочая группа: " + GetWorkgroup(), isSubItem: true);
                    WriteInfo("Название ОС: " + (os["Caption"] ?? "N/A"), isSubItem: true);
                    WriteInfo($"Версия ОС: {ver} (сборка {build}.{ubr})", isSubItem: true);
                    WriteInfo("Дата установки ОС: " +
                        (os["InstallDate"] != null ?
                        ManagementDateTimeConverter.ToDateTime(os["InstallDate"].ToString()).ToString("dd.MM.yyyy HH:mm") : "N/A"), isSubItem: true);
                    WriteInfo("Ключ продукта: " + GetProductKey(), isSubItem: true);
                    WriteInfo("ID продукта: " + (os["SerialNumber"] ?? "N/A"), isSubItem: true);
                    WriteInfo("DirectX: " + GetDirectXVersion(), isSubItem: true);
                    WriteInfo(".NET Framework: " + GetDotNetVersion(), isSubItem: true);

                    if (os["LastBootUpTime"] != null)
                    {
                        DateTime bootTime = ManagementDateTimeConverter.ToDateTime(os["LastBootUpTime"].ToString());
                        WriteInfo("Время загрузки: " + bootTime.ToString("dd.MM.yyyy в HH:mm"), isSubItem: true);

                        // Расчет времени работы с момента последней загрузки
                        TimeSpan uptime = DateTime.Now - bootTime;
                        WriteInfo("Время работы: " + FormatTimeSpan(uptime), isSubItem: true);
                    }
                    WriteInfo("", isHeader: false);
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации об ОС: " + ex.Message);
            }
        }

        // GetDirectXVersion запускает dxdiag для получения актуальной версии DirectX
        private static string GetDirectXVersion()
        {
            try
            {
                string tempFile = Path.Combine(Path.GetTempPath(), "dxdiag_output.txt");
                Process process = new();
                process.StartInfo.FileName = "dxdiag.exe";
                process.StartInfo.Arguments = $"/t {tempFile}";
                process.StartInfo.UseShellExecute = false;  // Запускает dxdiag в фоновом режиме, сохраняя вывод в текстовый файл
                process.StartInfo.CreateNoWindow = true;
                process.Start();
                process.WaitForExit();

                if (File.Exists(tempFile))
                {
                    string[] lines = File.ReadAllLines(tempFile);
                    foreach (string line in lines)
                    {
                        if (line.Contains("DirectX Version:"))
                        {
                            int colonIndex = line.IndexOf(':');
                            if (colonIndex != -1)
                            {
                                string version = line.Substring(colonIndex + 1).Trim();
                                version = version.Replace("DirectX ", "").Trim();
                                File.Delete(tempFile);
                                return version;
                            }
                        }
                    }
                    File.Delete(tempFile);
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка: " + ex.Message);
            }
            return "N/A";
        }

        // GetDotNetVersion определяет версию .NET Framework 4x по значению Release в реестре
        private static string GetDotNetVersion()
        {
            // Читаем из реестра релиз .NET Framework 4.x
            object relObj = Registry.GetValue(
                @"HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full", "Release", null);
            if (relObj is int r)
            {
                // Таблица соответствий релиз → версия
                if (r >= 533320) return "4.8.1";
                if (r >= 528040) return "4.8";
                if (r >= 461808) return "4.7.2";
                if (r >= 461308) return "4.7.1";
                if (r >= 460798) return "4.7";
                if (r >= 394802) return "4.6.2";
                if (r >= 394254) return "4.6.1";
                if (r >= 393295) return "4.6";
                if (r >= 379893) return "4.5.2";
                if (r >= 378675) return "4.5.1";
                if (r >= 378389) return "4.5";
            }
            return "N/A";
        }

        // BaseBoardInfo собирает информацию о системной плате (модель, производитель)
        private static void BaseBoardInfo()
        {
            try
            {
                WriteInfo("Системная плата:", true);
                var board = new ManagementObjectSearcher(
                    "SELECT Manufacturer, Product FROM Win32_BaseBoard")
                    .Get().Cast<ManagementObject>().FirstOrDefault();
                WriteInfo("Производитель: " + (board?["Manufacturer"]?.ToString() ?? "N/A"), isSubItem: true);
                WriteInfo("Модель: " + (board?["Product"]?.ToString() ?? "N/A"), isSubItem: true);

                try
                {
                    // Получает количество физических слотов для установки памяти
                    var cnt = new ManagementObjectSearcher("SELECT MemoryDevices FROM Win32_PhysicalMemoryArray")
                        .Get().Cast<ManagementObject>().FirstOrDefault()?["MemoryDevices"];
                    WriteInfo("Разъёмов ОЗУ: " + (cnt?.ToString() ?? "N/A"), isSubItem: true);
                }
                catch { WriteInfo("Разъёмов ОЗУ: N/A", isSubItem: true); }

                WriteInfo("", isHeader: false);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации о системной плате: " + ex.Message);
            }
        }

        // SharedResources собирает информацию о пользовательских общих папках, исключая административные
        private static void SharedResources()
        {
            WriteInfo("Общие ресурсы:", true);
            var searcher = new ManagementObjectSearcher(
                "SELECT Name, Path FROM Win32_Share WHERE Type = 0");
            foreach (ManagementObject share in searcher.Get().Cast<ManagementObject>())
            {
                string name = share["Name"]?.ToString() ?? "";
                string path = share["Path"]?.ToString() ?? "";

                // Исключает административные и системные общие ресурсы (оканчивающиеся на $)
                if (!name.EndsWith("$"))
                    WriteInfo($"Имя: \"{name}\", Путь: \"{path}\"", isSubItem: true);
            }
            WriteInfo("", isHeader: false);
        }

        // ProcessorInfo собирает и записывает данные о процессоре (ядра, кэш, температура)
        private static void ProcessorInfo()
        {
            try
            {
                var searcher = new ManagementObjectSearcher("SELECT * FROM Win32_Processor");
                foreach (ManagementObject proc in searcher.Get().Cast<ManagementObject>())
                {
                    string cpuName = proc["Name"]?.ToString() ?? "N/A";
                    string socket = proc["SocketDesignation"]?.ToString() ?? "N/A";
                    WriteInfo("Процессор:", true);
                    WriteInfo("Модель: " + cpuName, isSubItem: true);
                    WriteInfo("Сокет: " + socket, isSubItem: true);
                    WriteInfo("Ядер: " + (proc["NumberOfCores"] ?? "N/A"), isSubItem: true);
                    WriteInfo("Потоков: " + (proc["NumberOfLogicalProcessors"] ?? "N/A"), isSubItem: true);
                    WriteInfo("Кэш L2: " + (proc["L2CacheSize"] != null ? proc["L2CacheSize"] + " Кб" : "N/A"), isSubItem: true);
                    WriteInfo("Кэш L3: " + (proc["L3CacheSize"] != null ? proc["L3CacheSize"] + " Мб" : "N/A"), isSubItem: true);
                    
                    string cpuTemp = GetHardwareTemperature(HardwareType.Cpu, "CPU Package");
                    // Если температура CPU от 70°C, выделяет жирным красным цветом в отчёте
                    bool isHighTemp = cpuTemp != "N/A" && float.TryParse(cpuTemp.Replace("°C", ""), out float temp) && temp >= 70;
                    WriteInfo($"Температура: {cpuTemp}", isSubItem: true, subItemClass: $"processor {(isHighTemp ? "highlight-red" : "")}");
                    WriteInfo("", isHeader: false);
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации о процессоре: " + ex.Message);
            }
        }

        // VideoControllerInfo собирает информацию о видеокарте, включая тип и температуру
        private static void VideoControllerInfo()
        {
            try
            {
                var searcher = new ManagementObjectSearcher("SELECT * FROM Win32_VideoController");
                foreach (ManagementObject video in searcher.Get().Cast<ManagementObject>())
                {
                    string videoName = video["Name"]?.ToString() ?? "N/A";

                    // Определяет, является ли видеокарта встроенной или дискретной, на основе имени
                    string videoType = videoName.Contains("Intel") || videoName.Contains("AMD APU") ? "Встроенная" : "Дискретная";
                    WriteInfo("Видеокарта:", true);
                    WriteInfo("Тип: " + videoType, isSubItem: true);
                    WriteInfo("Видеопроцессор: " + videoName, isSubItem: true);

                    string adapterRAM = video["AdapterRAM"]?.ToString() ?? "N/A";

                    // Переводит размер памяти из байтов в Гигабайты
                    double memoryGB = adapterRAM != "N/A" && double.TryParse(adapterRAM, out double ramBytes)
                        ? Math.Round(ramBytes / (1024.0 * 1024 * 1024), 1)
                        : 0;
                    WriteInfo("Объём памяти: " + (memoryGB > 0 ? memoryGB + " Гб" : "N/A"), isSubItem: true);

                    // Пытается получить температуру GPU, последовательно проверяя датчики для разных вендоров
                    string gpuTemp = GetHardwareTemperature(HardwareType.GpuNvidia, "GPU Core");
                    gpuTemp ??= GetHardwareTemperature(HardwareType.GpuAmd, "GPU Core");
                    gpuTemp ??= GetHardwareTemperature(HardwareType.GpuIntel, "GPU Core");

                    // Если температура видеоядра от 70°C, выделяет жирным красным цветом в отчёте
                    bool isHighTemp = gpuTemp != "N/A" && float.TryParse(gpuTemp.Replace("°C", ""), out float temp) && temp >= 70;
                    WriteInfo($"Температура: {gpuTemp}", isSubItem: true, subItemClass: $"video {(isHighTemp ? "highlight-red" : "")}");
                    WriteInfo("", isHeader: false);
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации о видеокарте: " + ex.Message);
            }
        }

        // RAMInfo собирает и записывает информацию об оперативной памяти (объем, тип, SPD)
        private static void RAMInfo()
        {
            WriteInfo("ОЗУ:", true);
            double totalMemoryGB = 0, freeMemoryGB = 0;
            try
            {
                // Получает общий и свободный объем памяти в Мегабайтах
                using var os = new ManagementObjectSearcher(
                    "SELECT TotalVisibleMemorySize, FreePhysicalMemory FROM Win32_OperatingSystem");
                foreach (ManagementObject obj in os.Get().Cast<ManagementObject>())
                {
                    totalMemoryGB = Convert.ToDouble(obj["TotalVisibleMemorySize"]) / 1048576;
                    freeMemoryGB = Convert.ToDouble(obj["FreePhysicalMemory"]) / 1048576;
                    break;
                }
            }
            catch { }

            string memoryType = "Неизвестный";
            try
            {
                // Пытается определить тип памяти (DDRx) на основе SMBIOSMemoryType или MemoryType
                using var phys = new ManagementObjectSearcher("SELECT * FROM Win32_PhysicalMemory");
                foreach (ManagementObject mem in phys.Get().Cast<ManagementObject>())
                {
                    if (mem["SMBIOSMemoryType"] != null)
                    {
                        uint sm = Convert.ToUInt32(mem["SMBIOSMemoryType"]);
                        string? t = sm switch
                        {
                            0x12 => "DDR",
                            0x13 => "DDR2",
                            0x14 => "DDR2 FB-DIMM",
                            0x18 => "DDR3",
                            0x19 => "FBD2",
                            0x1A => "DDR4",
                            0x1B => "LPDDR",
                            _ => null
                        };
                        if (t != null)
                        {
                            memoryType = t;
                            break;
                        }
                    }
                    if (mem["MemoryType"] != null)
                    {
                        uint mt = Convert.ToUInt32(mem["MemoryType"]);
                        string? t = mt switch
                        {
                            20 => "DDR",
                            21 => "DDR2",
                            22 => "DDR2 FB-DIMM",
                            24 => "DDR3",
                            26 => "DDR4",
                            27 => "DDR5",
                            _ => null
                        };
                        if (t != null)
                        {
                            memoryType = t;
                            break;
                        }
                    }
                    uint speed = Convert.ToUInt32(mem["Speed"] ?? 0);
                    string pn = (mem["PartNumber"]?.ToString() ?? "").ToUpper();
                    if (speed >= 800 && pn.Contains("DDR2")) { memoryType = "DDR2"; break; }
                    if (speed >= 1333 && pn.Contains("DDR3")) { memoryType = "DDR3"; break; }
                }
            }
            catch (Exception ex)
            {
                // Не логирует "Не найдено", так как DDR2 или DDR3 могу в большенстве случаев не определяться, без низкоуровневого доступа
                if (!ex.Message.Contains("Не найдено"))
                {
                    Logging.WriteToLogFile($"Ошибка определения типа памяти: {ex.Message}");
                }
            }

            WriteInfo($"Тип: {memoryType}", isSubItem: true, subItemClass: "ram");
            WriteInfo($"Объём: всего {totalMemoryGB:N1} ГБ, свободно {freeMemoryGB:N1} ГБ", isSubItem: true, subItemClass: "ram");

            try
            {
                using var searcher = new ManagementObjectSearcher("SELECT * FROM Win32_PhysicalMemory");
                int slot = 1;
                foreach (ManagementObject mem in searcher.Get().Cast<ManagementObject>())
                {
                    double capacity = Convert.ToDouble(mem["Capacity"]) / 1073741824;
                    uint speed = Convert.ToUInt32(mem["Speed"]);
                    string manufacturer = mem["Manufacturer"]?.ToString().Trim() ?? "Неизвестный";
                    string partNumber = mem["PartNumber"]?.ToString().Trim() ?? "Неизвестный";

                    // Если WMI не предоставил производителя, пытается определить его по PartNumber
                    if (manufacturer == "Неизвестный" && (memoryType == "DDR2" || memoryType == "DDR3"))
                        manufacturer = GetMemoryManufacturer(partNumber);

                    WriteInfo($"SPD #{slot++}: {capacity:N1} ГБ, {speed} МГц, Производитель: {manufacturer}, Парт-номер: {partNumber}", isSubItem: true, subItemClass: "ram");
                }
                if (slot == 1) WriteInfo("SPD: Данные недоступны", isSubItem: true, subItemClass: "ram");
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Ошибка SPD: {ex.Message}");
            }

            WriteInfo("", isHeader: false);
        }

        // GetMemoryManufacturer определяет производителя модуля ОЗУ по его Part Number
        private static string GetMemoryManufacturer(string partNumber)
        {
            // Удаляет пробелы и приводим к верхнему регистру для единообразия
            partNumber = partNumber.Trim().ToUpper();

            // Проверки для каждого производителя
            if (partNumber.StartsWith("M47")) return "Samsung";                                 //  M470 для DDR2, M471 для DDR3
            if (partNumber.StartsWith("HYMP") || partNumber.StartsWith("HMT")) return "Hynix";  // HYMP для DDR2, HMT для DDR3
            if (partNumber.StartsWith("KVR")) return "Kingston";                                // Единый парт-номер подходит как для DDR2, так и для DDR3
            if (partNumber.StartsWith("MT")) return "Micron";
            if (partNumber.StartsWith("EDC") || partNumber.StartsWith("EDE")) return "Elpida";  // DDR2
            if (partNumber.StartsWith("NT") || partNumber.StartsWith("NY")) return "Nanya";     // NT для DDR2, NY для DDR3
            if (partNumber.StartsWith("HYB") || partNumber.StartsWith("HYS")) return "Qimonda"; // DDR2
            if (partNumber.StartsWith("CM") || partNumber.StartsWith("VS")) return "Corsair";   // CM для DDR2, VS для DDR3
            if (partNumber.StartsWith("CT")) return "Crucial";
            if (partNumber.StartsWith("JM")) return "Transcend";
            if (partNumber.StartsWith("PSD") || partNumber.StartsWith("PV")) return "Patriot";  // PSD для DDR2, PV для DDR3
            if (partNumber.StartsWith("OCZ")) return "OCZ";
            if (partNumber.StartsWith("KL")) return "Kingmax";
            if (partNumber.StartsWith("PM")) return "Promos";
            if (partNumber.StartsWith("AENEON")) return "Aeneon";
            if (partNumber.StartsWith("GEIL")) return "GEIL";
            if (partNumber.StartsWith("MUSHKIN")) return "Mushkin";

            // Если не найдено, возвращает "Неизвестный"
            return "Неизвестный";
        }

        // DiskDrivesInfo собирает подробную информацию о физических дисках и их состоянии
        private static void DiskDrivesInfo()
        {
            try
            {
                WriteInfo("Диски:", true);

                // Получает основные параметры дисков, отсортированные по индексу
                var diskSearcher = new ManagementObjectSearcher(
                    "SELECT DeviceID, Index, Model, InterfaceType, Size FROM Win32_DiskDrive");
                var disks = diskSearcher
                    .Get()
                    .Cast<ManagementObject>()
                    .OrderBy(d => Convert.ToUInt32(d["Index"]))
                    .ToList();

                // Использует пространство имен MSFT_PhysicalDisk для определения типа диска (SSD/HDD)
                var storageScope = new ManagementScope(@"\\.\root\Microsoft\Windows\Storage");
                storageScope.Connect();
                var physQuery = new ObjectQuery(
                    "SELECT DeviceId, MediaType, SpindleSpeed FROM MSFT_PhysicalDisk");
                var physSearcher = new ManagementObjectSearcher(storageScope, physQuery);
                var physDisks = physSearcher
                    .Get()
                    .Cast<ManagementObject>()
                    .ToDictionary(
                        mo => mo["DeviceId"].ToString(),
                        mo => new
                        {
                            MediaType = Convert.ToUInt16(mo["MediaType"]),
                            SpindleSpeed = Convert.ToUInt32(mo["SpindleSpeed"])
                        }
                    );

                // Собирает температуру дисков, используя библиотеку LibreHardwareMonitor
                var storageTemps = new Dictionary<string, float>();
                var computer = new Computer { IsStorageEnabled = true };
                computer.Open();
                foreach (var hw in computer.Hardware)
                {
                    if (hw.HardwareType == HardwareType.Storage)
                    {
                        hw.Update();
                        var tempSensor = hw.Sensors
                            .FirstOrDefault(s => s.SensorType == SensorType.Temperature);
                        if (tempSensor?.Value != null)
                            storageTemps[hw.Name] = tempSensor.Value.Value;
                    }
                }
                computer.Close();

                int totalDisks = disks.Count;
                for (int i = 0; i < totalDisks; i++)
                {
                    var disk = disks[i];
                    uint index = (uint)(disk["Index"] ?? 0);
                    string deviceId = index.ToString();
                    string model = disk["Model"]?.ToString() ?? "N/A";
                    string iface = disk["InterfaceType"]?.ToString() ?? "";
                    double sizeGB = disk["Size"] is ulong sz
                        ? Math.Round(sz / 1024d / 1024d / 1024d, 1)
                        : 0;
                    string letters = GetDriveLetters(disk["DeviceID"]?.ToString());
                    bool isSystem = letters.Split(' ').Contains("C:");  // Определяет, содержит ли диск системный раздел 'C:'

                    string deviceType;
                    if (iface.Equals("USB", StringComparison.OrdinalIgnoreCase))
                    {
                        deviceType = "USB";
                    }
                    // Определяет тип диска на основе интерфейса или данных MSFT_PhysicalDisk, с запасным вариантом по имени модели
                    else if (physDisks.TryGetValue(deviceId, out var pd))
                    {
                        if (pd.MediaType == 4)
                            deviceType = "SSD";
                        else if (pd.MediaType == 3)
                            deviceType = "HDD";
                        else
                            deviceType = pd.SpindleSpeed == 0 ? "SSD" : "HDD";
                    }
                    else
                    {
                        deviceType = model.ToUpper().Contains("SSD") ? "SSD" : "HDD";
                    }

                    WriteInfo($"Диск {index} ({letters})", isSubItem: true, subItemClass: "disk");
                    WriteInfo($"Название: {model} ({deviceType})", isSubItem: true, subItemClass: "disk");
                    WriteInfo($"Емкость: {sizeGB} ГБ", isSubItem: true, subItemClass: "disk");

                    if (deviceType != "USB")
                    {
                        WriteInfo($"Системный диск: {(isSystem ? "Да" : "Нет")}", isSubItem: true, subItemClass: "disk");
                        var cleanName = model.Replace(" ATA Device", "").Trim();
                        if (storageTemps.TryGetValue(cleanName, out var t))
                        {
                            // Если температура HDD от 55°C, а SSD от 60°C, выделяет жирным красным цветом в отчёте
                            bool isHighTemp = (deviceType == "HDD" && t >= 55) || (deviceType == "SSD" && t >= 60);
                            WriteInfo($"Температура: {t}°C", isSubItem: true, subItemClass: $"disk {(isHighTemp ? "highlight-red" : "")}");
                        }
                        else
                        {
                            var fb = storageTemps.FirstOrDefault(kv => kv.Key.Contains(cleanName));
                            if (fb.Key != null)
                            {
                                // Если температура HDD более 55°C, а SSD более 60°C, выделяет жирным красным цветом в отчёте
                                bool isHighTemp = (deviceType == "HDD" && fb.Value >= 55) || (deviceType == "SSD" && fb.Value >= 60);
                                WriteInfo($"Температура: {fb.Value}°C", isSubItem: true, subItemClass: $"disk {(isHighTemp ? "highlight-red" : "")}");
                            }
                            else
                            {
                                WriteInfo("Температура: N/A", isSubItem: true, subItemClass: "disk");
                            }
                        }
                    }
                    // Добавляет абзац после каждого диска (кроме последнего), для лучшей читаемости отчёта
                    if (i < totalDisks - 1)
                    {
                        htmlContent.AppendLine("<br class='disk'>");
                    }
                }
                WriteInfo("", isHeader: false);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации о физических дисках: " + ex.Message);
            }
        }

        // GetDriveLetters определяет логические буквы, принадлежащие физическому диску
        private static string GetDriveLetters(string? diskDeviceId)
        {
            try
            {
                var partitionSearcher = new ManagementObjectSearcher($"ASSOCIATORS OF {{Win32_DiskDrive.DeviceID='{diskDeviceId}'}} WHERE AssocClass = Win32_DiskDriveToDiskPartition");
                string letters = "";
                foreach (ManagementObject partition in partitionSearcher.Get().Cast<ManagementObject>())
                {
                    var logicalSearcher = new ManagementObjectSearcher($"ASSOCIATORS OF {{Win32_DiskPartition.DeviceID='{partition["DeviceID"]}'}} WHERE AssocClass = Win32_LogicalDiskToPartition");
                    foreach (ManagementObject logical in logicalSearcher.Get().Cast<ManagementObject>())
                    {
                        letters += logical["DeviceID"] + " ";
                    }
                }
                return letters.Trim();
            }
            catch { }
            return "N/A";
        }

        // LogicalDisksInfo собирает и записывает информацию о логических разделах и их занятости
        private static void LogicalDisksInfo()
        {
            try
            {
                WriteInfo("Разделы:", true);
                static void ProcessDisks(string wmiQuery, string prefix)
                {
                    var searcher = new ManagementObjectSearcher(wmiQuery);
                    foreach (ManagementObject disk in searcher.Get().Cast<ManagementObject>())
                    {
                        string letter = disk["DeviceID"]?.ToString() ?? "N/A";
                        string fs = disk["FileSystem"]?.ToString() ?? "N/A";
                        double totalSizeGB = disk["Size"] != null
                            && double.TryParse(disk["Size"].ToString(), out double sz)
                            ? Math.Round(sz / 1024 / 1024 / 1024, 1)
                            : 0;
                        double freeSpaceGB = disk["FreeSpace"] != null
                            && double.TryParse(disk["FreeSpace"].ToString(), out double free)
                            ? Math.Round(free / 1024 / 1024 / 1024, 1)
                            : 0;
                        double usedSpaceGB = Math.Round(totalSizeGB - freeSpaceGB, 1);
                        string usedStr = usedSpaceGB == 0
                            ? "0"
                            : usedSpaceGB.ToString("0.0");

                        // Расчет процента занятого места
                        double usedPercent = totalSizeGB > 0
                            ? Math.Round((usedSpaceGB / totalSizeGB) * 100)
                            : 0;

                        string usageText = $"(Занято {usedPercent}%: {usedStr} ГБ)";

                        // Если раздел заполнен от 85%, выделяет жирным красным цветом в отчёте
                        bool isHighUsage = usedPercent >= 85;
                        string diskInfo = $"{prefix} {letter} {fs} из {totalSizeGB:0.0} ГБ " +
                        $"свободно {freeSpaceGB:0.0} ГБ " +
                        $"{(isHighUsage ? $"<span class='highlight-red'>{usageText}</span>" : usageText)}";
                        WriteInfo(diskInfo, isSubItem: true, subItemClass: "logical-disk");
                    }
                }

                // Обрабатывает локальные диски и USB-флешки отдельно
                ProcessDisks("SELECT * FROM Win32_LogicalDisk WHERE DriveType = 3", "Диск");
                ProcessDisks("SELECT * FROM Win32_LogicalDisk WHERE DriveType = 2", "USB Flash");
                WriteInfo("", isHeader: false);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения информации о разделах: " + ex.Message);
            }
        }

        // IPAddresses получает и записывает IPv4 и IPv6 адреса для всех активных адаптеров
        private static void IPAddresses()
        {
            try
            {
                var searcher = new ManagementObjectSearcher("SELECT * FROM Win32_NetworkAdapterConfiguration WHERE IPEnabled = True");
                StringBuilder ipv4List = new();
                StringBuilder ipv6List = new();
                foreach (ManagementObject net in searcher.Get().Cast<ManagementObject>())
                {
                    if (net["IPAddress"] is string[] ipAddresses && net["IPSubnet"] is string[] subnets)
                    {
                        for (int i = 0; i < ipAddresses.Length; i++)
                        {
                            string ip = ipAddresses[i];
                            string cidr;
                            if (ip.Contains(":"))
                                cidr = subnets[i];
                            // Для IPv4 преобразует маску подсети в формат CIDR
                            else
                                cidr = SubnetToCidr(subnets[i]);

                            if (ip.Contains(":"))
                                ipv6List.Append($"{ip}/{cidr}, ");
                            else
                                ipv4List.Append($"{ip}/{cidr}, ");
                        }
                    }
                }
                WriteInfo("IP-адреса:", true);
                WriteInfo("IPv4: " + ipv4List.ToString().TrimEnd(',', ' '), isSubItem: true, subItemClass: "ip");
                WriteInfo("IPv6: " + ipv6List.ToString().TrimEnd(',', ' '), isSubItem: true, subItemClass: "ip");
                WriteInfo("", isHeader: false);
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile("Ошибка получения IP-адресов: " + ex.Message);
            }
        }

        // SubnetToCidr преобразует маску подсети в нотацию CIDR (например, 255.255.255.0 в /24)
        private static string SubnetToCidr(string subnet)
        {
            try
            {
                uint mask = BitConverter.ToUInt32([.. IPAddress.Parse(subnet).GetAddressBytes().Reverse()], 0);
                int cidr = 0;

                // Подсчитывает количество установленных бит для определения CIDR
                while (mask != 0)
                {
                    cidr += (int)(mask & 1);
                    mask >>= 1;
                }
                return cidr.ToString();
            }
            catch { }
            return "N/A";
        }

        // GetHardwareTemperature получает показания температуры для указанного типа оборудования через LibreHardwareMonitor
        private static string GetHardwareTemperature(HardwareType hwType, string keySubstring)
        {
            try
            {
                string? result = null;
                Computer computer = new()
                {
                    // Включает необходимые датчики для получения данных о температурах
                    IsCpuEnabled = true,
                    IsGpuEnabled = true,
                    IsMotherboardEnabled = true,
                    IsStorageEnabled = true
                };
                computer.Open();
                foreach (var hardware in computer.Hardware)
                {
                    if (hardware.HardwareType == hwType)
                    {
                        hardware.Update();
                        foreach (var sensor in hardware.Sensors)
                        {
                            if (sensor.SensorType == SensorType.Temperature)
                            {
                                // Фильтрует датчики по типу и имени (например, ищет только "Core" или "Package")
                                if (keySubstring == null || sensor.Name.Contains(keySubstring))
                                {
                                    if (result == null)
                                        result = sensor.Value + "°C";
                                    else
                                        result += ", " + sensor.Value + "°C";
                                }
                            }
                        }
                    }
                }
                computer.Close();
                return result ?? "N/A";
            }
            catch
            {
                return "N/A";
            }
        }

        // Plural выбирает правильную форму слова в зависимости от числа (для склонения дней, часов)
        private static string Plural(int n, string one, string few, string many)
        {
            n = Math.Abs(n) % 100;
            int n1 = n % 10;
            if (n > 10 && n < 20) return many;
            if (n1 > 1 && n1 < 5) return few;
            if (n1 == 1) return one;
            return many;
        }

        // FormatTimeSpan преобразует TimeSpan в отформатированную строку с корректным склонением слов
        private static string FormatTimeSpan(TimeSpan ts)
        {
            int d = ts.Days, h = ts.Hours, m = ts.Minutes, s = ts.Seconds;
            return $"{d} {Plural(d, "день", "дня", "дней")} " +
                   $"{h} {Plural(h, "час", "часа", "часов")} " +
                   $"{m} {Plural(m, "минута", "минуты", "минут")} " +
                   $"{s} {Plural(s, "секунда", "секунды", "секунд")}";
        }
    }
}
