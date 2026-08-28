import type { Job, CreateJobRequest, ApiError } from "@/types"

const API_BASE = "/api/v1"

async function handleResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    const error: ApiError = await response.json()
    throw new Error(error.message || `HTTP ${response.status}`)
  }
  return response.json()
}

export async function getJobs(): Promise<Job[]> {
  const response = await fetch(`${API_BASE}/jobs`)
  return handleResponse<Job[]>(response)
}

export async function getJob(id: string): Promise<Job> {
  const response = await fetch(`${API_BASE}/jobs/${id}`)
  return handleResponse<Job>(response)
}

export async function createJob(job: CreateJobRequest): Promise<{ id: string }> {
  const response = await fetch(`${API_BASE}/jobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(job),
  })
  return handleResponse<{ id: string }>(response)
}

export async function deleteJob(id: string): Promise<void> {
  const response = await fetch(`${API_BASE}/jobs/${id}`, {
    method: "DELETE",
  })
  if (!response.ok) {
    const error: ApiError = await response.json()
    throw new Error(error.message || `HTTP ${response.status}`)
  }
}

export function getDownloadUrl(id: string): string {
  return `${API_BASE}/jobs/${id}/download`
}
