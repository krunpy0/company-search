import type { Metadata } from "next";
import Link from "next/link";
import { FeedbackButton } from "../feedback-dialog";
import { productName } from "../product";
import { SystemScreen } from "../system-screen";

export const metadata: Metadata = { title: `Помощь · ${productName}` };

const questions = [
  {
    title: "Как найти компанию или ИП?",
    answer: "В поиске введите название организации, ИНН, ОГРН или ОГРНИП. Если одному ИНН соответствует несколько регистраций, выберите нужную компанию или ИП по названию и регистрационному номеру.",
  },
  {
    title: "Откуда берутся сведения?",
    answer: "Основной источник — открытые выгрузки ФНС, в том числе реестр МСП и наборы финансовых и налоговых сведений. Посмотреть происхождение данных можно в разделе «Источники» карточки компании и в ссылках, приложенных к ответу чата.",
  },
  {
    title: "Что означает дата рядом с показателем?",
    answer: "Дата актуальности показывает, на какой день источник зафиксировал сведения. Финансовые показатели также относятся к указанному отчётному периоду. Сведения из разных наборов могут иметь разные даты актуальности.",
  },
  {
    title: "Почему некоторых компаний или показателей нет?",
    answer: "Открытые выгрузки ФНС покрывают часть записей ЕГРЮЛ и ЕГРИП. Показатель может отсутствовать, если его не опубликовали, он неприменим к этой организации или источник временно недоступен. Нулевые значения отображаются отдельно.",
  },
  {
    title: "Когда обновляется лимит сообщений?",
    answer: "В чате доступно пять принятых сообщений за сутки. Лимит обновляется в полночь по московскому времени.",
  },
];

export default function HelpPage() {
  return (
    <SystemScreen title="Помощь">
      <section className="system-section system-help-questions" aria-labelledby="help-questions">
        <h2 id="help-questions">Частые вопросы</h2>
        {questions.map(({ title, answer }) => (
          <details key={title}>
            <summary>{title}</summary>
            <p>{answer}</p>
          </details>
        ))}
      </section>
      <section className="system-section" aria-labelledby="help-feedback">
        <h2 id="help-feedback">Обратная связь</h2>
        <p>Канал поддержки пока недоступен. Вы можете подготовить и скопировать обращение.</p>
        <div className="system-actions">
          <FeedbackButton>Оставить отзыв</FeedbackButton>
          <FeedbackButton kind="technical">Сообщить о проблеме</FeedbackButton>
        </div>
      </section>
      <section className="system-section" aria-labelledby="documents">
        <h2 id="documents">Документы</h2>
        <ul className="system-document-links">
          <li><Link href="/privacy">Политика конфиденциальности</Link></li>
          <li><Link href="/terms">Пользовательское соглашение</Link></li>
        </ul>
      </section>
    </SystemScreen>
  );
}
