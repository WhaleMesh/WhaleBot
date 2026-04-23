<script>
  import { goto } from '../lib/route.js';

  const tools = [
    {
      id: 'docker-create',
      name: 'User Docker Manager',
      desc: 'Manage userdockers via /api/v1/tools/user-dockers (list/create/remove/restart/interface).',
      status: 'available',
    },
  ];
</script>

<h1>Tools</h1>
<p class="hint">Select a tool to open its dedicated test page.</p>

<div class="grid">
  {#each tools as t}
    <button class="card" on:click={() => goto('tool', { id: t.id })}>
      <div class="row">
        <h2>{t.name}</h2>
        <span class="status {t.status}">{t.status}</span>
      </div>
      <p>{t.desc}</p>
    </button>
  {/each}
</div>

<style>
  h1 { margin-top: 0; }
  .hint { color: #9aa3bb; margin-top: -0.25rem; margin-bottom: 1rem; }
  .grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.9rem; }
  .card {
    background: #151923;
    border: 1px solid #232838;
    border-radius: 10px;
    padding: 0.85rem 0.95rem;
    text-align: left;
    cursor: pointer;
    color: inherit;
  }
  .card:hover { border-color: #324163; background: #181d29; }
  .row { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
  h2 { margin: 0; font-size: 1rem; font-weight: 600; }
  p { margin: 0.55rem 0 0; color: #9aa3bb; font-size: 0.9rem; }
  .status { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.05em; padding: 0.16rem 0.45rem; border-radius: 999px; border: 1px solid #2d3448; color: #a9b4cc; }
  .status.available { color: #85d8a7; border-color: #2f6645; background: #163222; }
  @media (max-width: 900px) { .grid { grid-template-columns: 1fr; } }
</style>
