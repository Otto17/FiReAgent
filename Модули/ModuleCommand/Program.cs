// Copyright (c) 2025 Otto
// Лицензия: MIT (см. LICENSE)

using System;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Threading.Tasks;
using Microsoft.Win32;

namespace ModuleCommand
{
    internal class Program
    {
        private const string CurrentVersion = "26.11.25"; // Текущая версия ModuleCommand в формате "дд.мм.гг"

        internal static async Task Main(string[] args)
        {
            // Показывает версию ModuleCommand
            if (args.Length >= 1 && string.Equals(args[0], "--version", StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine($"Версия \"ModuleCommand\": {CurrentVersion}");
                return;
            }

            // Проверяет наличие аргументов (защита от ручного запуска)
            if (args.Length == 0)
            {
                Console.WriteLine("Модуль работает только в составе программы!");
                Logging.WriteToLogFile("Модуль работает только в составе программы!");
                return;
            }

            // Получает BaseTime из реестра и преобразует в HEX
            string regPath = @"SYSTEM\CurrentControlSet\Control\Session Manager\Memory Management\PrefetchParameters";
            object baseTimeObj = Registry.LocalMachine.OpenSubKey(regPath)?.GetValue("BaseTime");
            if (baseTimeObj == null)
            {
                Logging.WriteToLogFile("Не удалось получить данные из реестра.");
                return;
            }
            string baseTimeHex = Convert.ToInt32(baseTimeObj).ToString("x8");

            // Сверяет аргумент с BaseTime
            string baseTimeArg = args[0].Trim().ToLower();
            if (!baseTimeArg.Equals(baseTimeHex, StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine("Неверные данные в аргументе!");
                Logging.WriteToLogFile("Неверные данные в аргументе!");
                return;
            }

            // Получает имя Named Pipe из аргументов
            string pipeName = null;
            foreach (var arg in args)
            {
                if (arg.StartsWith("--pipename="))
                {
                    pipeName = arg.Substring("--pipename=".Length);
                    break;
                }
            }
            if (string.IsNullOrEmpty(pipeName))
            {
                Logging.WriteToLogFile("Ошибка: не указано имя Named Pipe.");
                return;
            }

            // Создаёт Named Pipe (однократное подключение) и ждет подключения
            using var pipeServer = new NamedPipeServerStream(pipeName, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous);
            Console.WriteLine("Ожидание подключения клиента к Named Pipe: " + pipeName);
            await pipeServer.WaitForConnectionAsync();

            // Чтение длины сообщения
            var lengthBytes = new byte[4];
            int bytesRead = await pipeServer.ReadAsync(lengthBytes, 0, 4);
            if (bytesRead < 4)
            {
                Logging.WriteToLogFile("Ошибка: не удалось прочитать длину сообщения.");
                return;
            }
            int length = BitConverter.ToInt32(lengthBytes, 0);

            // Чтение сообщения
            var messageBytes = new byte[length];
            bytesRead = await pipeServer.ReadAsync(messageBytes, 0, length);
            if (bytesRead < length)
            {
                Logging.WriteToLogFile("Ошибка: не удалось прочитать полное сообщение.");
                return;
            }
            string message = Encoding.UTF8.GetString(messageBytes);
            Console.WriteLine("Получено сообщение: " + message);

            // Обработка сообщения – создание и запуск задачи
            string taskResult = await Scheduler.CreateAndRunTaskAsync(message);

            try
            {
                // Отправка ответа через Named Pipe
                byte[] responseBytes = Encoding.UTF8.GetBytes(taskResult);
                byte[] responseLengthBytes = BitConverter.GetBytes(responseBytes.Length);
                await pipeServer.WriteAsync(responseLengthBytes, 0, responseLengthBytes.Length);
                await pipeServer.WriteAsync(responseBytes, 0, responseBytes.Length);
                await pipeServer.FlushAsync();
            }
            catch (IOException ex)
            {
                Logging.WriteToLogFile("Ошибка при отправке ответа через Named Pipe: " + ex.Message);
                // Не выбрасывает исключение, чтобы завершить работу модуля корректно
            }
        }
    }
}
