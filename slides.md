---
theme: default
title: YOLO Runner
titleTemplate: '%s'
info: |
  Preliminary Slidev deck based on keynote.md.
aspectRatio: 16/9
transition: slide-left
mdc: true
drawings:
  persist: false
---

<style>
:root {
  --yr-ink: #1f2937;
  --yr-muted: #5b6473;
  --yr-purple: #6d5bd0;
  --yr-blue: #3f7ad6;
  --yr-green: #2f8f5b;
  --yr-gold: #c38a1b;
  --yr-cream: #faf8f2;
}

.slidev-layout {
  color: var(--yr-ink);
  background: var(--yr-cream);
}

.slidev-layout h1,
.slidev-layout h2,
.slidev-layout h3 {
  letter-spacing: -0.02em;
}

.deck-kicker {
  display: inline-block;
  margin-bottom: 1rem;
  padding: 0.25rem 0.7rem;
  border: 1px solid rgba(109, 91, 208, 0.35);
  border-radius: 999px;
  font-size: 0.85rem;
  color: var(--yr-purple);
  background: rgba(109, 91, 208, 0.08);
}

.hero-title {
  font-size: 3.1rem;
  line-height: 1;
  font-weight: 700;
  margin: 0.3rem 0 0.8rem;
}

.hero-subtitle {
  font-size: 1.25rem;
  line-height: 1.35;
  max-width: 42rem;
  color: var(--yr-ink);
}

.muted {
  color: var(--yr-muted);
}

.card-grid {
  display: grid;
  gap: 1rem;
  margin-top: 1.25rem;
}

.grid-2 {
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.grid-3 {
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.idea-card {
  padding: 1rem 1.1rem;
  border-radius: 18px;
  background: rgba(255, 255, 255, 0.74);
  border: 1px solid rgba(31, 41, 55, 0.12);
  box-shadow: 0 10px 30px rgba(31, 41, 55, 0.06);
}

.idea-card h3,
.idea-card h4 {
  margin: 0 0 0.6rem;
  font-size: 1.08rem;
}

.idea-card ul {
  margin: 0;
  padding-left: 1.1rem;
}

.idea-card li + li {
  margin-top: 0.35rem;
}

.accent-purple {
  border-color: rgba(109, 91, 208, 0.3);
  background: linear-gradient(180deg, rgba(109, 91, 208, 0.09), rgba(255, 255, 255, 0.78));
}

.accent-blue {
  border-color: rgba(63, 122, 214, 0.3);
  background: linear-gradient(180deg, rgba(63, 122, 214, 0.08), rgba(255, 255, 255, 0.78));
}

.accent-green {
  border-color: rgba(47, 143, 91, 0.3);
  background: linear-gradient(180deg, rgba(47, 143, 91, 0.08), rgba(255, 255, 255, 0.78));
}

.accent-gold {
  border-color: rgba(195, 138, 27, 0.32);
  background: linear-gradient(180deg, rgba(195, 138, 27, 0.09), rgba(255, 255, 255, 0.78));
}

.big-line {
  font-size: 1.35rem;
  line-height: 1.45;
  max-width: 46rem;
}

.mini-note {
  margin-top: 0.9rem;
  font-size: 0.92rem;
  color: var(--yr-muted);
}

.soft-list li + li {
  margin-top: 0.45rem;
}
</style>

---
layout: cover
background: radial-gradient(circle at top left, rgba(109,91,208,0.18), transparent 28%), radial-gradient(circle at top right, rgba(47,143,91,0.14), transparent 24%), linear-gradient(180deg, #fbfaf7 0%, #f3efe6 100%)
class: text-left
---

<div class="deck-kicker">Черновик доклада</div>

<div class="hero-title">YOLO Runner</div>

<div class="hero-subtitle">
Как я собираю себе систему для долгой автономной работы кодинг-агентов:
через задачи в трекере, оркестратор и раннеры.
</div>

<div class="mini-note">
Предварительная версия на основе <code>keynote.md</code>.
</div>

---

# Кто я

<div class="idea-card accent-purple">
  <h3>Заготовка для короткого интро</h3>
  <ul class="soft-list">
    <li><b>[Имя, команда, роль]</b></li>
    <li>[Чем занимаетесь в обычной жизни]</li>
    <li>[Почему вам вообще понадобилась такая система]</li>
  </ul>
</div>

<div class="mini-note">
Сюда можно вставить короткую подводку на 20-30 секунд, когда появится финальный контекст выступления.
</div>

---
layout: section
---

# Зачем это всё

---

# Проблема

<div class="card-grid grid-3">
  <div class="idea-card accent-purple">
    <h3>Дефицит внимания</h3>
    <p>Постоянное переключение между окнами и задачами ломает фокус и мешает возвращаться к важному.</p>
  </div>
  <div class="idea-card accent-blue">
    <h3>Слишком много ручного контроля</h3>
    <p>Нужно бесконечно подтверждать, уточнять, принимать решения и держать всё в голове.</p>
  </div>
  <div class="idea-card accent-green">
    <h3>Ночные лимиты простаивают</h3>
    <p>Дорогие и полезные токены есть, но они не превращаются в длинную автономную работу.</p>
  </div>
</div>

---

# Мои интересы очень специфичны

<div class="card-grid grid-3">
  <div class="idea-card accent-gold">
    <h3>Долгая работа без меня</h3>
    <p>Хочется, чтобы система продолжала двигаться по задачам, пока я сплю или занят другим.</p>
  </div>
  <div class="idea-card accent-blue">
    <h3>Контроль через трекер</h3>
    <p>Управление должно идти через привычные задачи и статусы, а не через отдельный чат-ритуал.</p>
  </div>
  <div class="idea-card accent-green">
    <h3>Разные агенты</h3>
    <p>Нужна возможность выбирать модель, инструменты и раннер под конкретный тип работы.</p>
  </div>
</div>

---

# Почему не что-то готовое

<div class="card-grid grid-2">
  <div class="idea-card accent-purple">
    <h3>Мои задачи довольно нишевые</h3>
    <ul class="soft-list">
      <li>длинные автономные прогоны</li>
      <li>жесткая привязка к трекеру задач</li>
      <li>несколько типов раннеров и агентов</li>
    </ul>
  </div>
  <div class="idea-card accent-blue">
    <h3>Это еще и учебный проект</h3>
    <ul class="soft-list">
      <li>понять, как устроены такие системы внутри</li>
      <li>пощупать реальные ограничения и компромиссы</li>
      <li>собрать свой слой управления поверх агентов</li>
    </ul>
  </div>
</div>

---
layout: section
---

# Как это устроено

---

# Три главные части

<div class="card-grid grid-3">
  <div class="idea-card accent-blue">
    <h3>Хранилище задач</h3>
    <p>Хранит задачи, статусы и связи между ними.</p>
  </div>
  <div class="idea-card accent-purple">
    <h3>Агент-оркестратор</h3>
    <p>Получает текущее состояние, выбирает runnable-задачи и управляет исполнением.</p>
  </div>
  <div class="idea-card accent-green">
    <h3>Раннеры</h3>
    <p>Запускают конкретных кодинг-агентов и доводят выполнение до результата.</p>
  </div>
</div>

---

# Верхнеуровневая схема

```mermaid
flowchart LR
  Storage["Хранилище задач"]
  Orchestrator["Агент-оркестратор"]
  Runners["Раннеры"]
  Agents["Кодинг-агенты"]
  Repo["Репозиторий / task clones"]
  Monitor["Мониторинг / yolo-tui"]

  Storage --> Orchestrator
  Orchestrator --> Runners
  Runners --> Agents
  Runners --> Repo
  Orchestrator --> Monitor
```

---

# Хранилище задач

<div class="card-grid grid-2">
  <div class="idea-card accent-blue">
    <h3>Что оно хранит</h3>
    <ul class="soft-list">
      <li>сами задачи</li>
      <li>статусы</li>
      <li>родительские связи</li>
      <li>зависимости между задачами</li>
    </ul>
  </div>
  <div class="idea-card accent-gold">
    <h3>Зачем это важно</h3>
    <ul class="soft-list">
      <li>можно запускать только то, что действительно готово к выполнению</li>
      <li>можно управлять всем через привычный трекер</li>
      <li>система не живет отдельной жизнью от задач</li>
    </ul>
  </div>
</div>

<div class="mini-note">В текущей реализации это могут быть TK, GitHub, Linear, beads/br.</div>

---

# Агент-оркестратор

<div class="idea-card accent-purple">
  <ul class="soft-list">
    <li>получает текущее дерево задач</li>
    <li>решает, что можно запускать прямо сейчас</li>
    <li>раздает задачи раннерам в нужном порядке</li>
    <li>контролирует прогресс, логи, проверки и завершение работы</li>
  </ul>
</div>

<div class="mini-note">Именно здесь живет вся логика «что делать дальше».</div>

---

# Раннер

<div class="card-grid grid-2">
  <div class="idea-card accent-green">
    <h3>Что делает</h3>
    <ul class="soft-list">
      <li>запускает backend кодинг-агента</li>
      <li>общается через ACP, CLI или app server</li>
      <li>выполняет задачу в рабочей копии репозитория</li>
    </ul>
  </div>
  <div class="idea-card accent-gold">
    <h3>Что для меня важно</h3>
    <ul class="soft-list">
      <li>можно подменять модель и инструменты</li>
      <li>можно делать YOLO-режим там, где это уместно</li>
      <li>можно сравнивать поведение разных агентов</li>
    </ul>
  </div>
</div>

---
layout: section
---

# Что самое важное

---

# Что реально влияет на качество

<div class="card-grid grid-2">
  <div class="idea-card accent-purple">
    <h3>Размер и формулировка задач</h3>
    <p class="big-line">Если задача слишком большая, слишком расплывчатая или плохо декомпозирована, агент почти неизбежно начнет ошибаться.</p>
  </div>
  <div class="idea-card accent-green">
    <h3>Среда и инструменты</h3>
    <p class="big-line">Качество зависит не только от модели, но и от доступа к коду, тестам, git, логам и понятному контуру выполнения.</p>
  </div>
</div>

<div class="mini-note">Модель важна. Но плохая задача и плохая среда ломают результат быстрее, чем выбор бренда модели.</div>

---
layout: section
---

# Что дальше

---

# Roadmap

<div class="card-grid grid-2">
  <div class="idea-card accent-blue">
    <h3>Больше раннеров</h3>
    <ul class="soft-list">
      <li>разные модели</li>
      <li>разные инструменты</li>
      <li>разные execution profiles</li>
    </ul>
  </div>
  <div class="idea-card accent-green">
    <h3>Распределенное выполнение</h3>
    <ul class="soft-list">
      <li>подключение раннеров по сети</li>
      <li>параллельная работа на отдельных машинах</li>
    </ul>
  </div>
  <div class="idea-card accent-gold">
    <h3>Безопасность</h3>
    <ul class="soft-list">
      <li>усиление sandbox-модели</li>
      <li>возможно контейнеризация</li>
      <li>более аккуратная передача секретов</li>
    </ul>
  </div>
  <div class="idea-card accent-purple">
    <h3>Оргмодель</h3>
    <ul class="soft-list">
      <li>BYOT / shared ownership</li>
      <li>использование не только для одного проекта</li>
    </ul>
  </div>
</div>

---
layout: center
class: text-center
---

# Спасибо

### Вопросы, идеи, возражения

<div class="mini-note">
Следующий шаг для этого черновика: добавить один живой кейс, короткое демо и финальный слайд «что уже работает сегодня».
</div>
