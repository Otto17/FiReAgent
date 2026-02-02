// Copyright (c) 2025-2026 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.Diagnostics;
using System.IO;

namespace ModuleInfo
{
    internal class CompressXZ
    {
        // Compress_XZ сжимает файл с помощью "7z.exe", в формате "xz" с максимальной степенью сжатия
        internal static void Compress_XZ(string fileName)
        {
            try
            {
                // Путь к выходному архиву
                string compressedFile = fileName + ".xz";

                // Проверяет существование исходного файла
                if (!File.Exists(fileName))
                {
                    Logging.WriteToLogFile($"Файл {fileName} не найден!");
                    return;
                }

                // Путь к папке с портативным 7-Zip
                string basePath = AppDomain.CurrentDomain.BaseDirectory;
                string sevenZipPath = Path.Combine(basePath, "tool\\7z", "7z.exe");

                // Проверяет существование 7z.exe
                if (!File.Exists(sevenZipPath))
                {
                    Logging.WriteToLogFile($"Файл '7z.exe' не найден в папке: {Path.GetDirectoryName(sevenZipPath)}");
                    return;
                }

                // Настраивает параметры xz:
                // -txz - формат xz (использует LZMA2)
                // -m0=LZMA2 - алгоритм LZMA2 (основной для xz)
                // -mx=9 - максимальный уровень сжатия
                string arguments = $"a -txz -m0=LZMA2 -mx=9 \"{compressedFile}\" \"{fileName}\"";

                // Настраивает процесс
                ProcessStartInfo processInfo = new()
                {
                    FileName = sevenZipPath,
                    Arguments = arguments,
                    RedirectStandardOutput = true,
                    RedirectStandardError = true,
                    UseShellExecute = false,
                    CreateNoWindow = true
                };

                // Запускает процесс сжатия
                using Process process = Process.Start(processInfo);
                string output = process.StandardOutput.ReadToEnd();
                string error = process.StandardError.ReadToEnd();
                process.WaitForExit();

                if (process.ExitCode == 0)
                {
                   // Console.WriteLine($"Файл успешно сжат в {compressedFile}");
                    File.Delete(fileName); // Удаляет исходный файл после успешного сжатия
                }
                else
                {
                    Logging.WriteToLogFile($"Ошибка сжатия: {output}");
                    if (!string.IsNullOrEmpty(error))
                    {
                        Logging.WriteToLogFile($"Дополнительная информация: {error}");
                    }
                }
            }
            catch (Exception ex)
            {
                Logging.WriteToLogFile($"Произошла ошибка: {ex.Message}");
            }
        }
    }
}
