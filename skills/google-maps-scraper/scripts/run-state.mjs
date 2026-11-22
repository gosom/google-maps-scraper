import { execFileSync } from "node:child_process";

export function processIsAlive(pid) {
  if (!Number.isSafeInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    if (error.code === "ESRCH") return false;
    // Permission errors do not prove that a process has stopped.
    throw error;
  }
}

export function inspectContainer(container) {
  try {
    return JSON.parse(execFileSync("docker", ["inspect", "--format", "{{json .State}}", container], {
      encoding: "utf8", stdio: ["ignore", "pipe", "pipe"],
    }));
  } catch (error) {
    const stderr = String(error.stderr || "").trim();
    if (stderr === `Error: No such object: ${container}` ||
        stderr === `Error: No such container: ${container}` ||
        stderr === `Error response from daemon: No such container: ${container}`) return null;
    throw error;
  }
}

export function reconcileState(state) {
  if (state.backend === "docker") {
    const container = inspectContainer(state.container);
    if (!container) return state.status === "exited" ? state : { ...state, status: "interrupted" };
    return {
      ...state,
      status: container.Status,
      ...(container.Status === "exited" ? { exit_code: container.ExitCode, finished_at: container.FinishedAt } : {}),
    };
  }
  if (state.status !== "exited" && !processIsAlive(state.worker_pid) && !processIsAlive(state.pid)) {
    return { ...state, status: "interrupted" };
  }
  return state;
}
