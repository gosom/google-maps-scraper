export interface Job {
  id: string
  name: string
  date: string
  status: "pending" | "working" | "ok" | "failed"
  data: JobData
}

export interface JobData {
  keywords: string[]
  lang: string
  zoom: number
  lat: string
  lon: string
  fast_mode: boolean
  radius: number
  depth: number
  email: boolean
  extra_reviews: boolean
  max_time: number
  proxies: string[]
}

export interface CreateJobRequest {
  name: string
  keywords: string[]
  lang: string
  zoom: number
  lat: string
  lon: string
  fast_mode: boolean
  radius: number
  depth: number
  email: boolean
  extra_reviews: boolean
  max_time: number
  proxies: string[]
}

export interface Place {
  title: string
  address: string
  latitude: number
  longitude: number
  rating?: number
  reviews?: number
  phone?: string
  website?: string
  category?: string
}

export interface ApiError {
  code: number
  message: string
}
