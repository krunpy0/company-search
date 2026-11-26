import type { ReactNode } from "react";
import Link from "next/link";
import { Arrow, NoticeButton, QuestionForm } from "./landing-controls";
import { productName } from "./product";

type DemoVariant = "search" | "analysis" | "followup";

const features: { title: ReactNode; description: ReactNode; demo: DemoVariant }[] = [
  { title: <>Найдите компанию <br />без ИНН</>, description: "Достаточно названия или имени предпринимателя.", demo: "search" },
  { title: <>Разберитесь <br />в рисках</>, description: <>Выводы со ссылками <br />на источники.</>, demo: "analysis" },
  { title: <>Уточните детали <br />в чате</>, description: "Продолжайте диалог по той же компании.", demo: "followup" },
];

const planRows = [
  ["Запросов в день", "5", "100"],
  ["Официальные данные", "Включены", "Включены"],
  ["Приоритетная обработка", "—", "Включена"],
  ["Пополнение кредитов", "—", "Доступно"],
];

function Brand() {
  return <a className="brand" href="#top">{productName}</a>;
}

function DemoInput({ children, attach = false }: { children: ReactNode; attach?: boolean }) {
  return (
    <div className="demo-input">
      {attach && <span className="demo-attach">+</span>}
      <span>{children}</span>
      {!attach && <span className="demo-attach">+</span>}
      <span className="demo-send"><Arrow /></span>
    </div>
  );
}

function ChatDemo({ variant }: { variant: DemoVariant }) {
  return (
    <div className={`chat-demo chat-${variant}`} aria-hidden="true">
      <div className="demo-sidebar">
        <div className="demo-toolbar">
          <strong>ScanINN</strong>
          <span className="demo-search-icon" />
          <span className="demo-panel-icon" />
        </div>
        <div className="demo-new">+ Новый чат</div>
        <small>Ваши чаты</small>
        <ul>
          <li>Анализ Сбербанка</li>
          <li>Проверка Яндекса</li>
          <li>Финансы МТС</li>
          <li>Проверка поставщика</li>
        </ul>
      </div>
      <div className="demo-main">
        {variant === "search" && <>
          <h4>Какую компанию изучим?</h4>
          <p>Найдите компанию, проверьте конкурента или задайте вопрос.</p>
          <DemoInput>Введите ИНН, ОГРН, название компании или ФИО предпринимателя...</DemoInput>
        </>}
        {variant === "analysis" && <>
          <p className="demo-bubble">Проанализируй Сбербанк</p>
          <p>Вот человеческое описание компании на основе предоставленных данных:</p>
          <h4>Краткая сводка</h4>
          <p>ПАО Сбербанк — один из крупнейших банков России. История банка начинается в <strong>1841 году</strong>. Основные направления — банковские услуги для физических лиц и бизнеса.</p>
        </>}
        {variant === "followup" && <>
          <p className="demo-bubble">Кто является основным владельцем компании?</p>
          <p>Основной акционер ПАО Сбербанк — Российская Федерация в лице Минфина России. Ей принадлежит 50% плюс одна акция от общего числа акций банка. Это подтверждается промежуточной отчётностью Сбера за сентябрь 2026 года.</p>
          <DemoInput attach>Ответить...</DemoInput>
        </>}
      </div>
    </div>
  );
}

export default function Home() {
  return (
    <>
      <a className="skip-link" href="#content">К основному содержимому</a>
      <header className="hero" id="top">
        <nav className="navigation" aria-label="Основная навигация">
          <Brand />
          <div className="nav-links">
            <a href="#features">Возможности</a>
            <a href="#pricing">Тарифы</a>
            <a href="#business">Для бизнеса</a>
          </div>
          <Link className="button button-primary nav-start" href="/chat?start=1" prefetch={false}>Начать</Link>
        </nav>
        <div className="hero-content" id="content" tabIndex={-1}>
          <h1>Узнай всё о контрагенте</h1>
          <p className="hero-description">Анализируйте компании, проверяйте контрагентов и выявляйте риски с помощью ИИ</p>
          <QuestionForm />
        </div>
      </header>
      <main>
        <section className="features" id="features" aria-labelledby="features-heading">
          <h2 id="features-heading">Всё, что нужно знать о бизнесе</h2>
          <p className="section-subtitle">Анализ компаний по официальным данным</p>
          <div className="feature-list">
            {features.map(({ title, description, demo }) => (
              <article className={`feature feature-${demo}`} key={demo}>
                <div className="feature-copy">
                  <h3>{title}</h3>
                  <p>{description}</p>
                </div>
                <ChatDemo variant={demo} />
              </article>
            ))}
          </div>
        </section>
        <section className="pricing" id="pricing" aria-labelledby="pricing-heading">
          <h2 id="pricing-heading">Выберите свой тариф</h2>
          <div className="pricing-table">
            <table aria-describedby="pricing-note">
              <thead>
                <tr>
                  <td />
                  <th scope="col">Бесплатно<span className="price">0 ₽ <small>/ месяц</small></span></th>
                  <th scope="col">Pro<span className="price">1 950 ₽ <small>/ месяц</small></span></th>
                </tr>
              </thead>
              <tbody>
                {planRows.map(([label, free, pro]) => (
                  <tr key={label}><th scope="row">{label}</th><td>{free}</td><td>{pro}</td></tr>
                ))}
              </tbody>
              <tfoot>
                <tr>
                  <td />
                  <td><Link className="button button-outline" href="/chat?start=1" prefetch={false}>Начать бесплатно</Link></td>
                  <td><NoticeButton variant="primary" title="Тариф Pro" message="Цена и условия тарифа ожидают подтверждения. Подключение пока недоступно.">Подключить Pro</NoticeButton></td>
                </tr>
              </tfoot>
            </table>
          </div>
          <p className="pricing-note" id="pricing-note">Цена и условия Pro ожидают подтверждения.</p>
        </section>
      </main>
      <footer className="footer">
        <div className="footer-inner">
          <section className="closing" id="business" aria-labelledby="closing-heading">
            <h2 id="closing-heading">Узнайте больше о своём контрагенте</h2>
            <Link className="button button-primary" href="/chat?start=1" prefetch={false}>Начать анализ <Arrow direction="right" /></Link>
          </section>
          <div className="footer-links">
            <div>
              <Brand />
              <p>Аналитика бизнеса<br />на основе официальных<br />данных.</p>
            </div>
            <div>
              <h3>Продукт</h3>
              <ul>
                <li><a href="#features">Возможности</a></li>
                <li><a href="#pricing">Тарифы</a></li>
                <li><a href="#business">Для бизнеса</a></li>
              </ul>
            </div>
            <div>
              <h3>Информация</h3>
              <ul>
                <li><Link href="/help">Поддержка</Link></li>
                <li><Link href="/help#documents">Документы</Link></li>
                <li><a href="#top">О сервисе</a></li>
              </ul>
            </div>
          </div>
          <p className="copyright">© 2026 {productName}. Все права защищены.</p>
        </div>
      </footer>
    </>
  );
}
