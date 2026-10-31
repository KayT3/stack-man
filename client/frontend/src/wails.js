// Thin wrappers around the Wails-generated bindings. Keeping them in one place
// means the Svelte components never touch window.go directly.

function backend() {
  if (!window.go || !window.go.main || !window.go.main.App) {
    throw new Error('Wails bindings are not available (run this inside the desktop app)');
  }
  return window.go.main.App;
}

export const ListAgents = () => backend().ListAgents();
export const AgentInfo = (agentID) => backend().AgentInfo(agentID);
export const ListStacks = (agentID) => backend().ListStacks(agentID);
export const StackServices = (agentID, stack) => backend().StackServices(agentID, stack);
export const GitStatus = (agentID, stack) => backend().GitStatus(agentID, stack);
export const GitCheck = (agentID, stack) => backend().GitCheck(agentID, stack);
export const GitPull = (agentID, stack) => backend().GitPull(agentID, stack);
export const ContainersUp = (agentID, stack, opts) => backend().ContainersUp(agentID, stack, opts);
export const ContainersDown = (agentID, stack, opts) => backend().ContainersDown(agentID, stack, opts);
export const ContainersRestart = (agentID, stack, services) => backend().ContainersRestart(agentID, stack, services);
export const ContainersBuild = (agentID, stack, services) => backend().ContainersBuild(agentID, stack, services);
export const ContainersPs = (agentID, stack) => backend().ContainersPs(agentID, stack);
export const ContainersLogs = (agentID, stack, service, follow) =>
  backend().ContainersLogs(agentID, stack, service, follow);
export const ListCommands = (agentID, stack) => backend().ListCommands(agentID, stack);
export const RunCommand = (agentID, stack, command, params) =>
  backend().RunCommand(agentID, stack, command, params);

export function onStream(handler) {
  if (!window.runtime || !window.runtime.EventsOn) {
    return () => {};
  }
  return window.runtime.EventsOn('stream', handler);
}
