<script>
  import { onMount, tick } from 'svelte';
  import * as api from './wails.js';

  let agents = [];
  let agent = null;
  let stacks = [];
  let stack = null;
  let services = [];
  let commands = [];
  let containers = [];
  let check = null;
  let selectedCommand = null;
  let paramValues = {};
  let output = '';
  let busy = false;
  let error = '';
  let upBuild = true;
  let downVolumes = false;
  let followLogs = false;
  let selectedServices = [];
  let jobs = [];
  let jobTimer;
  let outputEl;

  $: runningJobs = jobs.filter((j) => !stack || j.stack === stack.name);

  onMount(() => {
    const off = api.onStream(handleStream);
    run('scan agents', refreshAgents);
    jobTimer = setInterval(refreshJobs, 4000);
    return () => {
      clearInterval(jobTimer);
      if (typeof off === 'function') off();
    };
  });

  async function refreshJobs() {
    if (!agent) return;
    try {
      jobs = (await api.Jobs(agent.id)) || [];
    } catch (e) {
      // Discovery/connection errors are surfaced by explicit actions; polling
      // should stay quiet.
    }
  }

  function handleStream(notification) {
    if (!notification || !notification.method) return;
    if (notification.method === 'stream.log') {
      const data = notification.params && notification.params.data;
      if (data) append(data);
    } else if (notification.method === 'stream.step') {
      const p = notification.params || {};
      append(`\n[step ${p.step}/${p.total}] ${p.label || ''}\n`);
    }
  }

  function append(text) {
    output = (output + text).slice(-200000);
    tick().then(() => {
      if (outputEl) outputEl.scrollTop = outputEl.scrollHeight;
    });
  }

  async function run(label, fn) {
    busy = true;
    error = '';
    append(`\n$ ${label}\n`);
    try {
      await fn();
    } catch (e) {
      error = typeof e === 'string' ? e : e && e.message ? e.message : String(e);
      append(`error: ${error}\n`);
    } finally {
      busy = false;
      refreshJobs();
    }
  }

  async function refreshAgents() {
    agents = (await api.ListAgents()) || [];
    if (agents.length && !agent) {
      await chooseAgent(agents[0]);
    } else if (agent) {
      agent = agents.find((a) => a.id === agent.id) || agent;
    }
  }

  async function chooseAgent(next) {
    agent = next;
    stack = null;
    stacks = [];
    services = [];
    commands = [];
    containers = [];
    check = null;
    selectedCommand = null;
    try {
      const info = await api.AgentInfo(next.id);
      agent = { ...agent, hostname: info.hostname || agent.hostname, reachable: true };
      await refreshStacks();
    } catch (e) {
      error = e && e.message ? e.message : String(e);
    }
  }

  async function refreshStacks() {
    if (!agent) return;
    stacks = (await api.ListStacks(agent.id)) || [];
    if (stack) stack = stacks.find((s) => s.name === stack.name) || null;
  }

  async function chooseStack(next) {
    stack = next;
    check = null;
    selectedCommand = null;
    selectedServices = [];
    await run(`inspect ${next.name}`, async () => {
      services = (await api.StackServices(agent.id, next.name)) || [];
      commands = (await api.ListCommands(agent.id, next.name)) || [];
      containers = [];
      await refreshJobs();
    });
  }

  async function doCheck() {
    await run(`git check ${stack.name}`, async () => {
      check = await api.GitCheck(agent.id, stack.name);
    });
  }

  async function doPull() {
    await run(`git pull ${stack.name}`, async () => {
      const result = await api.GitPull(agent.id, stack.name);
      check = await api.GitCheck(agent.id, stack.name);
      await refreshStacks();
      if (!result.pulled) append('already up to date\n');
    });
  }

  async function doUp() {
    await run(`containers up ${stack.name}${targetSuffix()}`, async () => {
      await api.ContainersUp(agent.id, stack.name, {
        build: upBuild,
        force_recreate: false,
        remove_orphans: false,
        services: selectedServices
      });
      await afterContainerOp();
    });
  }

  async function doBuild() {
    await run(`containers build ${stack.name}${targetSuffix()}`, async () => {
      await api.ContainersBuild(agent.id, stack.name, selectedServices);
      await afterContainerOp();
    });
  }

  async function doRestart() {
    await run(`containers restart ${stack.name}${targetSuffix()}`, async () => {
      await api.ContainersRestart(agent.id, stack.name, selectedServices);
      await afterContainerOp();
    });
  }

  async function doDown() {
    await run(`containers down ${stack.name}${targetSuffix()}`, async () => {
      await api.ContainersDown(agent.id, stack.name, { volumes: downVolumes, services: selectedServices });
      await afterContainerOp();
    });
  }

  async function afterContainerOp() {
    containers = (await api.ContainersPs(agent.id, stack.name)) || [];
    await refreshJobs();
  }

  function targetSuffix() {
    return selectedServices.length ? ` ${selectedServices.join(' ')}` : '';
  }

  async function doPs() {
    await run(`containers ps ${stack.name}`, async () => {
      containers = (await api.ContainersPs(agent.id, stack.name)) || [];
    });
  }

  async function doLogs() {
    if (!stack) return;
    const first = services.length ? services[0].service : '';
    await run(`containers logs ${stack.name}`, async () => {
      await api.ContainersLogs(agent.id, stack.name, first, followLogs);
    });
  }

  function pickCommand(command) {
    selectedCommand = command;
    paramValues = {};
    for (const p of command.params || []) {
      paramValues[p.name] = p.default || '';
    }
  }

  async function runSelectedCommand() {
    if (!selectedCommand) return;
    const params = {};
    for (const p of selectedCommand.params || []) params[p.name] = paramValues[p.name] || '';
    await run(`command run ${stack.name} ${selectedCommand.name}`, async () => {
      await api.RunCommand(agent.id, stack.name, selectedCommand.name, params);
    });
  }

  function policyLabel(policy) {
    if (policy === 'exclude') return 'excluded';
    if (policy === 'allow') return 'managed';
    return 'unspecified';
  }
</script>

<div class="shell">
  <header>
    <div>
      <h1>stackman</h1>
      <p class="muted">Compose stacks over your private subnet</p>
    </div>
    <div class="header-actions">
      <span class:busy class="dot"></span>
      <button on:click={() => run('scan agents', refreshAgents)} disabled={busy}>Rescan</button>
    </div>
  </header>

  {#if error}
    <div class="error">{error}</div>
  {/if}

  <main>
    <aside>
      <section>
        <h2>Agents</h2>
        {#if agents.length === 0}
          <p class="muted small">No agents yet. Check your <code>client.toml</code> static list or rescan.</p>
        {/if}
        {#each agents as a}
          <button class:active={agent && agent.id === a.id} class="row" on:click={() => run(`connect ${a.addr}`, () => chooseAgent(a))}>
            <span class="status" class:on={a.reachable}></span>
            <span class="grow">
              <strong>{a.hostname || a.id || a.addr}</strong>
              <small>{a.addr} · {a.source}</small>
            </span>
            <span class="count">{a.stacks ? a.stacks.length : 0}</span>
          </button>
        {/each}
      </section>

      {#if agent}
        <section>
          <h2>Stacks</h2>
          {#if stacks.length === 0}
            <p class="muted small">No stacks registered on this agent.</p>
          {/if}
          {#each stacks as s}
            <button class:active={stack && stack.name === s.name} class="row" on:click={() => chooseStack(s)}>
              <span class="grow">
                <strong>{s.name}</strong>
                <small>{s.branch || 'unknown branch'}</small>
              </span>
            </button>
          {/each}
        </section>
      {/if}
    </aside>

    <section class="detail">
      {#if !stack}
        <div class="empty">
          <h2>Pick a stack</h2>
          <p class="muted">Choose an agent, then a stack, to see services, git status and commands.</p>
        </div>
      {:else}
        <div class="stack-head">
          <div>
            <h2>{stack.name}</h2>
            <p class="muted small">{stack.path} · {stack.branch || 'unknown branch'} · {stack.remote || 'no remote'}</p>
          </div>
          <div class="actions">
            <button on:click={doCheck} disabled={busy}>Check</button>
            <button class="primary" on:click={doPull} disabled={busy || (check && !check.safe_to_pull)}>Pull</button>
          </div>
        </div>

        {#if check}
          <div class="check" class:safe={check.safe_to_pull}>
            <strong>{check.safe_to_pull ? 'SAFE to pull' : 'UNSAFE to pull'}</strong>
            <span>ahead {check.ahead} · behind {check.behind} · {check.clean ? 'clean' : 'dirty'}{check.diverged ? ' · diverged' : ''}</span>
            {#if check.changed_files && check.changed_files.length}
              <ul>
                {#each check.changed_files as file}
                  <li>{file}</li>
                {/each}
              </ul>
            {/if}
          </div>
        {/if}

        <div class="columns">
          <div class="card">
            <h3>Services</h3>
            <ul class="services">
              {#each services as service}
                <li>
                  <span class="name">{service.service}</span>
                  <span class="policy {service.policy || 'none'}">{policyLabel(service.policy)}</span>
                </li>
              {/each}
            </ul>
          </div>

          <div class="card">
            <h3>Containers</h3>
            <p class="muted small">Pick services for Up / Build / Restart / Down. None selected means all managed services.</p>
            <div class="picker">
              {#each services as service}
                <label class="chip" class:off={service.policy === 'exclude'}>
                  <input
                    type="checkbox"
                    bind:group={selectedServices}
                    value={service.service}
                    disabled={service.policy === 'exclude'}
                  />
                  {service.service}
                </label>
              {/each}
            </div>
            <div class="actions wrap">
              <label class="check-inline"><input type="checkbox" bind:checked={upBuild} /> build on up</label>
              <button on:click={doUp} disabled={busy}>Up</button>
              <button class="primary" on:click={doBuild} disabled={busy}>Build</button>
              <button on:click={doRestart} disabled={busy}>Restart</button>
              <button on:click={doDown} disabled={busy}>Down</button>
              <label class="check-inline"><input type="checkbox" bind:checked={downVolumes} /> volumes</label>
              <button on:click={doPs} disabled={busy}>Ps</button>
              <label class="check-inline"><input type="checkbox" bind:checked={followLogs} /> follow</label>
              <button on:click={doLogs} disabled={busy}>Logs</button>
            </div>
            {#if runningJobs.length}
              <div class="jobs">
                {#each runningJobs as job}
                  <span class="job"><span class="spinner"></span>{job.command} · {job.elapsed_seconds}s</span>
                {/each}
                <span class="muted small">keeps running if you disconnect</span>
              </div>
            {/if}
            <ul class="containers">
              {#each containers as c}
                <li>
                  <span class="name">{c.service}</span>
                  <span class="muted small">{c.state} · {c.status}</span>
                </li>
              {/each}
            </ul>
          </div>
        </div>

        <div class="card">
          <h3>Commands</h3>
          <div class="commands">
            {#each commands as command}
              <button class:active={selectedCommand && selectedCommand.name === command.name} on:click={() => pickCommand(command)}>
                {command.name}
              </button>
            {/each}
          </div>
          {#if selectedCommand}
            <p class="muted small">{selectedCommand.description}</p>
            {#if selectedCommand.params && selectedCommand.params.length}
              <div class="params">
                {#each selectedCommand.params as param}
                  <label>
                    <span>{param.name}{param.required ? ' *' : ''}</span>
                    <input bind:value={paramValues[param.name]} placeholder={param.description || ''} />
                  </label>
                {/each}
              </div>
            {/if}
            <button class="primary" on:click={runSelectedCommand} disabled={busy}>Run {selectedCommand.name}</button>
          {/if}
        </div>

        <div class="card output-card">
          <div class="output-head">
            <h3>Output</h3>
            <button class="ghost" on:click={() => (output = '')}>Clear</button>
          </div>
          <pre bind:this={outputEl}>{output || 'Run a command to see live output.'}</pre>
        </div>
      {/if}
    </section>
  </main>
</div>

<style>
  :global(:root) {
    color-scheme: dark;
    --bg: #0d1117;
    --panel: #161b22;
    --panel-2: #1c2330;
    --border: #2a3441;
    --text: #e6edf3;
    --muted: #8b98a5;
    --accent: #4f9cff;
    --green: #3fb950;
    --red: #f85149;
    --amber: #d29922;
  }
  :global(*) { box-sizing: border-box; }
  :global(body) {
    margin: 0;
    background: var(--bg);
    color: var(--text);
    font-family: ui-sans-serif, system-ui, -apple-system, 'Segoe UI', sans-serif;
    font-size: 14px;
  }
  :global(button) {
    background: var(--panel-2);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 12px;
    cursor: pointer;
    font: inherit;
  }
  :global(button:hover:not(:disabled)) { border-color: var(--accent); }
  :global(button:disabled) { opacity: 0.45; cursor: not-allowed; }
  :global(button.primary) { background: var(--accent); border-color: var(--accent); color: #06121f; font-weight: 600; }
  :global(button.ghost) { background: transparent; border-color: transparent; color: var(--muted); }
  :global(input[type='text'], input:not([type])) {
    background: var(--bg); color: var(--text); border: 1px solid var(--border);
    border-radius: 6px; padding: 6px 8px; font: inherit; width: 100%;
  }
  :global(code) { background: var(--panel-2); padding: 1px 4px; border-radius: 4px; }

  .shell { min-height: 100vh; display: flex; flex-direction: column; }
  header {
    display: flex; justify-content: space-between; align-items: center;
    padding: 14px 22px; border-bottom: 1px solid var(--border); background: var(--panel);
  }
  h1 { font-size: 18px; margin: 0; letter-spacing: 0.4px; }
  h2 { font-size: 14px; margin: 0 0 10px; text-transform: uppercase; letter-spacing: 0.8px; color: var(--muted); }
  h3 { font-size: 14px; margin: 0 0 10px; }
  .muted { color: var(--muted); }
  .small { font-size: 12px; }
  .header-actions { display: flex; align-items: center; gap: 10px; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--green); }
  .dot.busy { background: var(--amber); animation: pulse 1s infinite alternate; }
  @keyframes pulse { from { opacity: 0.3; } to { opacity: 1; } }

  main { display: grid; grid-template-columns: 280px 1fr; min-height: 0; flex: 1; }
  aside { border-right: 1px solid var(--border); padding: 18px; overflow-y: auto; }
  aside section { margin-bottom: 26px; }
  .row {
    width: 100%; display: flex; align-items: center; gap: 8px; text-align: left;
    background: transparent; border: 1px solid transparent; padding: 8px; margin-bottom: 4px;
  }
  .row.active { background: var(--panel); border-color: var(--border); }
  .row .grow { display: flex; flex-direction: column; flex: 1; min-width: 0; }
  .row small { color: var(--muted); font-size: 11px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .status { width: 8px; height: 8px; border-radius: 50%; background: #3a4654; }
  .status.on { background: var(--green); }
  .count { color: var(--muted); font-size: 11px; }

  .detail { padding: 22px; overflow-y: auto; }
  .empty { margin-top: 20vh; text-align: center; }
  .stack-head { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 16px; }
  .stack-head h2 { color: var(--text); font-size: 22px; text-transform: none; letter-spacing: 0; margin: 0; }
  .actions { display: flex; gap: 8px; }
  .actions.wrap { flex-wrap: wrap; align-items: center; }
  .check-inline { display: inline-flex; align-items: center; gap: 5px; color: var(--muted); font-size: 12px; }

  .check {
    border: 1px solid var(--border); border-left: 3px solid var(--red);
    background: var(--panel); padding: 10px 14px; border-radius: 6px; margin-bottom: 16px;
    display: flex; gap: 14px; align-items: baseline; flex-wrap: wrap;
  }
  .check.safe { border-left-color: var(--green); }
  .check ul { width: 100%; margin: 6px 0 0; padding-left: 18px; color: var(--muted); font-family: ui-monospace, monospace; font-size: 12px; }

  .columns { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .card { background: var(--panel); border: 1px solid var(--border); border-radius: 8px; padding: 16px; margin-bottom: 16px; }
  ul { list-style: none; margin: 0; padding: 0; }
  .services li, .containers li { display: flex; justify-content: space-between; gap: 10px; padding: 6px 0; border-bottom: 1px solid var(--border); }
  .services li:last-child, .containers li:last-child { border-bottom: 0; }
  .policy { font-size: 11px; padding: 2px 7px; border-radius: 999px; border: 1px solid var(--border); color: var(--muted); }
  .policy.allow { color: var(--green); border-color: rgba(63, 185, 80, 0.4); }
  .policy.exclude { color: var(--amber); border-color: rgba(210, 153, 34, 0.4); }

  .picker { display: flex; flex-wrap: wrap; gap: 6px; margin: 8px 0 12px; }
  .chip {
    display: inline-flex; align-items: center; gap: 5px; cursor: pointer;
    border: 1px solid var(--border); border-radius: 999px; padding: 3px 10px;
    font-size: 12px; color: var(--muted);
  }
  .chip:has(input:checked) { color: var(--text); border-color: var(--accent); background: rgba(79, 156, 255, 0.12); }
  .chip.off { opacity: 0.4; cursor: not-allowed; }
  .jobs { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin: 10px 0; font-size: 12px; }
  .job {
    display: inline-flex; align-items: center; gap: 6px; color: var(--accent);
    background: rgba(79, 156, 255, 0.12); border: 1px solid rgba(79, 156, 255, 0.35);
    border-radius: 999px; padding: 3px 10px;
  }
  .spinner { width: 10px; height: 10px; border-radius: 50%; border: 2px solid rgba(79, 156, 255, 0.3); border-top-color: var(--accent); animation: spin 0.8s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .commands { display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 10px; }
  .commands button.active { border-color: var(--accent); color: var(--accent); }
  .params { display: grid; gap: 8px; margin-bottom: 12px; max-width: 460px; }
  .params label { display: grid; gap: 4px; font-size: 12px; color: var(--muted); }

  .output-card { display: flex; flex-direction: column; }
  .output-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
  .output-head h3 { margin: 0; }
  pre {
    margin: 0; background: #06090d; border: 1px solid var(--border); border-radius: 6px;
    padding: 12px; height: 260px; overflow: auto; white-space: pre-wrap; word-break: break-word;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 12px; color: #c9d1d9;
  }
  .error { margin: 0 22px; margin-top: 14px; padding: 10px 14px; border-radius: 6px; background: rgba(248, 81, 73, 0.12); border: 1px solid rgba(248, 81, 73, 0.4); color: #ffb3ad; }

  @media (max-width: 900px) {
    main { grid-template-columns: 1fr; }
    aside { border-right: 0; border-bottom: 1px solid var(--border); }
    .columns { grid-template-columns: 1fr; }
  }
</style>
