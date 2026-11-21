<script lang="ts">
  import { onMount } from 'svelte';

  type State = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled' | 'timed_out';
  type Job = { id: string; sequence: number; task_type: string; state: State; created_at: string; started_at: string | null; completed_at: string | null; exit_code: number | null; error: string };
  const labels: Record<State, string> = { queued: '排队中', running: '运行中', succeeded: '已完成', failed: '失败', cancelled: '已取消', timed_out: '已超时' };
  const secure = window.location.protocol === 'https:';
  let checking = $state(true);
  let authenticated = $state(false);
  let token = $state('');
  let csrf = '';
  let expires = $state('');
  let busy = $state(false);
  let message = $state('');
  let jobs = $state<Job[]>([]);
  let selected = $state<Job | null>(null);
  let output = $state('');
  let clipped = $state(false);
  let after = $state(0);
  let cursors = $state<number[]>([]);
  let nextAfter = 0;
  let offset = $state(0);
  let decoder = new TextDecoder();
  let logComplete = $state(false);
  let generation = 0;
  let polling = $state(false);
  let alive = true;
  const terminal = (state: State) => state !== 'queued' && state !== 'running';
  const date = (value: string | null) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—';

  function reset() {
    generation++; authenticated = false; csrf = ''; expires = ''; token = '';
    jobs = []; selected = null; output = ''; clipped = false; offset = 0; after = 0; cursors = [];
  }

  async function api(path: string, init: RequestInit = {}) {
    const requestGeneration = generation;
    const headers = new Headers(init.headers);
    if (init.method && init.method !== 'GET' && csrf) headers.set('X-CSRF-Token', csrf);
    const response = await fetch('/api/v1' + path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' });
    if (!response.ok) {
      if (response.status === 401 && authenticated && requestGeneration === generation) { reset(); throw new Error('会话已失效，请重新登录。'); }
      const error = await response.json().catch(() => null);
      throw new Error(response.status === 401 ? '令牌无效，请重新输入。' : error?.error?.message || `请求失败 (${response.status})`);
    }
    return response;
  }

  function selectJob(job: Job) {
    generation++; selected = job; output = ''; offset = 0; clipped = false;
    decoder = new TextDecoder(); logComplete = false; message = '';
    void refresh();
  }

  function appendLog(text: string) {
    output += text;
    if (output.length > 200_000) { output = output.slice(-200_000); clipped = true; }
  }

  async function refresh() {
    if (!authenticated || polling || !alive) return;
    polling = true;
    const current = generation;
    try {
      const page = await (await api(`/jobs?after=${after}&limit=20`)).json();
      if (!alive || current !== generation) return;
      jobs = page.data; nextAfter = page.next_after;
      if (!selected) return;
      const id = selected.id;
      const detail = await (await api(`/jobs/${id}`)).json();
      if (!alive || current !== generation) return;
      selected = detail.data;
      for (let chunk = 0; chunk < 4 && !logComplete; chunk++) {
        const response = await api(`/jobs/${id}/logs?offset=${offset}&limit=65536`);
        const data = await response.arrayBuffer();
        if (!alive || current !== generation) return;
        offset = Number(response.headers.get('X-Next-Offset'));
        appendLog(decoder.decode(data, { stream: true }));
        if (data.byteLength < 65536) {
          if (selected && terminal(selected.state)) { appendLog(decoder.decode()); logComplete = true; }
          break;
        }
      }
    } catch (error) {
      if (alive && (current === generation || !authenticated)) message = (error as Error).message;
    } finally { polling = false; }
  }

  async function login(event: SubmitEvent) {
    event.preventDefault(); busy = true; message = '';
    try {
      const response = await api('/session', { method: 'POST', headers: { Authorization: `Bearer ${token.trim()}` } });
      const { data } = await response.json();
      csrf = data.csrf_token; expires = data.expires_at; authenticated = true; generation++;
      await refresh();
    } catch (error) { message = (error as Error).message; }
    finally { token = ''; busy = false; }
  }

  async function logout() {
    busy = true; message = '';
    try { await api('/session', { method: 'DELETE' }); reset(); }
    catch (error) { message = (error as Error).message; }
    finally { busy = false; }
  }

  async function submit() {
    busy = true; message = '';
    try {
      const response = await api('/jobs', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ task_type: 'system-info', parameters: {} }) });
      const { data } = await response.json();
      selectJob(data);
    } catch (error) { message = (error as Error).message; }
    finally { busy = false; }
  }

  function nextPage() { cursors = [...cursors, after]; after = nextAfter; generation++; void refresh(); }
  function previousPage() { after = cursors.at(-1) ?? 0; cursors = cursors.slice(0,-1); generation++; void refresh(); }

  onMount(() => {
    alive = true;
    void (async () => {
      try {
        const response = await fetch('/api/v1/session', { credentials: 'same-origin', cache: 'no-store' });
        if (!alive) return;
        if (response.ok) {
          const { data } = await response.json();
          if (!alive) return;
          csrf = data.csrf_token; expires = data.expires_at; authenticated = true;
          await refresh();
        } else if (response.status !== 401) message = '暂时无法连接服务，请刷新重试。';
      } catch { if (alive) message = '无法连接服务，请确认 RemoteDeck 正在运行。'; }
      finally { if (alive) checking = false; }
    })();
    const timer = window.setInterval(() => { if (!document.hidden) void refresh(); }, 1500);
    return () => { alive = false; generation++; window.clearInterval(timer); };
  });
</script>

<svelte:head><meta name="description" content="RemoteDeck 本地任务控制台" /></svelte:head>

<div class="shell">
  <header class="topbar">
    <a class="brand" href="/" aria-label="RemoteDeck 首页"><span class="brand-mark" aria-hidden="true">R<span>↗</span></span><span>RemoteDeck<small>任务控制台</small></span></a>
    <div class="top-actions"><span class="local-tag"><span></span> {secure ? 'HTTPS 连接' : '本机连接'}</span>{#if authenticated}<button class="quiet" onclick={logout} disabled={busy}>退出登录</button>{/if}</div>
  </header>

  {#if checking}
    <main class="login-layout"><p role="status">正在检查会话…</p></main>
  {:else if !authenticated}
    <main class="login-layout">
      <section class="intro"><p class="eyebrow">YOUR TASKS. YOUR MACHINE.</p><h1>让任务继续，<br />把结果留住。</h1><p class="intro-copy">提交任务，查看执行状态，随时回来读取日志。<br />从第一项任务开始。</p><div class="flow-note"><span>01 提交</span><i>→</i><span>02 执行</span><i>→</i><span>03 查看结果</span></div></section>
      <form class="login-card" onsubmit={login}>
        <span class="card-kicker">连接工作台</span><h2>欢迎回来</h2><p>输入服务访问令牌，开启一个安全会话。</p>
        <label for="access-token">访问令牌</label><input id="access-token" type="password" bind:value={token} required autocomplete="off" spellcheck="false" placeholder="粘贴访问令牌" aria-describedby="token-help" disabled={busy} />
        <p id="token-help" class="help">令牌位于服务数据目录中的 <code>auth.token</code>。令牌不会保存到浏览器本地存储。</p>
        {#if message}<p class="error" role="alert">{message}</p>{/if}
        <button class="primary wide" type="submit" disabled={busy}>{busy ? '正在连接…' : '进入控制台'} <span aria-hidden="true">→</span></button>
        <p class="login-foot">会话 8 小时后失效 · {secure ? '加密连接' : '仅限本机访问'}</p>
      </form>
    </main>
  {:else}
    <main class="dashboard">
      <div class="page-heading"><div><p class="eyebrow">WORKSPACE / {secure ? 'HTTPS' : 'LOCAL'}</p><h1>任务工作台</h1><p>执行有记录，结果可追溯。</p></div><div class="session-note">会话有效至<br /><strong>{date(expires)}</strong></div></div>
      {#if message}<p class="error page-error" role="alert">{message}</p>{/if}
      <section class="submit-card" aria-labelledby="submit-title"><div class="task-icon" aria-hidden="true">&gt;_</div><div class="task-description"><span class="card-kicker">预定义任务</span><h2 id="submit-title">系统信息</h2><p>读取 Linux 内核与主机信息，输出保存为任务日志。</p></div><div class="task-meta"><code>system-info</code><span>最长 10 秒 · 顺序执行</span></div><button class="primary" onclick={submit} disabled={busy}>{busy ? '处理中…' : '提交任务'} <span aria-hidden="true">↗</span></button></section>
      <div class="workspace-grid">
        <section class="panel history" aria-labelledby="history-title">
          <div class="panel-heading"><div><span class="card-kicker">HISTORY</span><h2 id="history-title">任务记录 <span class="count">{jobs.length}</span></h2></div><span class="muted">从早到晚</span></div>
          <div class="job-list">
            {#each jobs as job (job.id)}
              <button class:chosen={selected?.id === job.id} class="job-row" onclick={() => selectJob(job)} aria-pressed={selected?.id === job.id}>
                <div class="job-row-top"><strong>系统信息 <span class="sequence">#{job.sequence}</span></strong><span class="badge" class:succeeded={job.state==='succeeded'} class:failed={job.state==='failed'||job.state==='timed_out'} class:running={job.state==='running'}>{labels[job.state]}</span></div>
                <div class="job-row-bottom"><time datetime={job.created_at}>{date(job.created_at)}</time><span aria-hidden="true">↗</span></div>
              </button>
            {:else}<div class="empty-state"><span class="empty-icon" aria-hidden="true">≡</span><h3>这里还没有任务</h3><p>提交一次「系统信息」，开始第一条执行记录。</p></div>{/each}
          </div>
          <div class="pagination"><button class="quiet" onclick={previousPage} disabled={polling || !cursors.length}>← 上一页</button><span>第 {cursors.length+1} 页</span><button class="quiet" onclick={nextPage} disabled={polling || jobs.length<20}>下一页 →</button></div>
        </section>
        <section class="panel detail" aria-labelledby="detail-title">
          <div class="panel-heading"><div><span class="card-kicker">INSPECT</span><h2 id="detail-title">执行详情</h2></div>{#if selected}<span class="muted">#{selected.sequence}</span>{/if}</div>
          {#if selected}
            <div class="detail-body"><div class="detail-status"><h3>系统信息</h3><span class="badge" class:succeeded={selected.state==='succeeded'} class:failed={selected.state==='failed'||selected.state==='timed_out'} class:running={selected.state==='running'}>{labels[selected.state]}</span></div><dl><div><dt>任务 ID</dt><dd class="job-id">{selected.id}</dd></div><div><dt>开始时间</dt><dd>{date(selected.started_at)}</dd></div><div><dt>结束时间</dt><dd>{date(selected.completed_at)}</dd></div><div><dt>退出码</dt><dd>{selected.exit_code ?? '—'}</dd></div></dl>{#if selected.error}<p class="error">{selected.error}</p>{/if}</div>
            <div class="log-heading"><span>输出日志</span><span>{logComplete ? '读取完成' : '自动更新'} · {offset.toLocaleString()} B</span></div>
            {#if clipped}<p class="clip-note">页面仅显示最后 200,000 个字符；完整日志保留在服务器。</p>{/if}
            <textarea class="log-output" readonly aria-label="任务输出日志" value={output || (terminal(selected.state) ? '暂无输出。' : '等待任务输出…')}></textarea>
          {:else}<div class="empty-state detail-empty"><span class="empty-icon" aria-hidden="true">↖</span><h3>选择一项任务</h3><p>查看状态、退出码和保存的输出日志。</p></div>{/if}
        </section>
      </div>
      <footer><span>RemoteDeck · 任务执行</span><span>状态每 1.5 秒更新 · 关闭页面不会取消任务</span></footer>
    </main>
  {/if}
</div>
