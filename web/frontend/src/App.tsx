import { useState, useEffect } from "react"
import { JobForm } from "@/components/JobForm"
import { JobTable } from "@/components/JobTable"
import { JobMapModal } from "@/components/JobMapModal"
import { getJobs, createJob } from "@/api/client"
import type { Job, CreateJobRequest, Place } from "@/types"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Github, ExternalLink, Loader2 } from "lucide-react"

function App() {
  const [jobs, setJobs] = useState<Job[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [selectedJob, setSelectedJob] = useState<{ id: string; name: string } | null>(null)
  const [places, setPlaces] = useState<Place[]>([])
  const [mapLoading, setMapLoading] = useState(false)
  const [mapError, setMapError] = useState<string | null>(null)

  const fetchJobs = async () => {
    try {
      const data = await getJobs()
      setJobs(data)
    } catch (err) {
      console.error("Failed to fetch jobs:", err)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchJobs()
    const interval = setInterval(fetchJobs, 10000)
    return () => clearInterval(interval)
  }, [])

  const handleSubmit = async (jobData: CreateJobRequest) => {
    setError(null)
    try {
      await createJob(jobData)
      await fetchJobs()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to create job")
      throw err
    }
  }

  const handleViewJob = async (job: Job) => {
    setSelectedJob({ id: job.id, name: job.name })
    setPlaces([])
    setMapError(null)
    setMapLoading(true)

    try {
      const response = await fetch(`/api/v1/jobs/${job.id}/download`)
      if (!response.ok) {
        throw new Error("Failed to load places")
      }
      const text = await response.text()
      const lines = text.split("\n").filter(Boolean)

      if (lines.length < 2) {
        setPlaces([])
        return
      }

      const headers = lines[0].split(",")
      const latIdx = headers.findIndex((h) => h.toLowerCase().includes("lat"))
      const lonIdx = headers.findIndex((h) => h.toLowerCase().includes("lon"))
      const titleIdx = headers.findIndex(
        (h) => h.toLowerCase().includes("title") || h.toLowerCase().includes("name")
      )
      const addressIdx = headers.findIndex((h) => h.toLowerCase().includes("address"))
      const ratingIdx = headers.findIndex((h) => h.toLowerCase().includes("rating"))
      const reviewsIdx = headers.findIndex((h) => h.toLowerCase().includes("reviews"))
      const phoneIdx = headers.findIndex((h) => h.toLowerCase().includes("phone"))
      const websiteIdx = headers.findIndex((h) => h.toLowerCase().includes("website"))

      const parsedPlaces: Place[] = []
      for (let i = 1; i < lines.length; i++) {
        const values = lines[i].split(",")
        if (latIdx >= 0 && lonIdx >= 0) {
          const lat = parseFloat(values[latIdx])
          const lon = parseFloat(values[lonIdx])
          if (!isNaN(lat) && !isNaN(lon)) {
            parsedPlaces.push({
              title: titleIdx >= 0 ? values[titleIdx] : "Unknown",
              address: addressIdx >= 0 ? values[addressIdx] : "",
              latitude: lat,
              longitude: lon,
              rating: ratingIdx >= 0 ? parseFloat(values[ratingIdx]) || undefined : undefined,
              reviews: reviewsIdx >= 0 ? parseInt(values[reviewsIdx]) || undefined : undefined,
              phone: phoneIdx >= 0 ? values[phoneIdx] : undefined,
              website: websiteIdx >= 0 ? values[websiteIdx] : undefined,
            })
          }
        }
      }
      setPlaces(parsedPlaces)
    } catch (err) {
      setMapError(err instanceof Error ? err.message : "Failed to load places")
    } finally {
      setMapLoading(false)
    }
  }

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b bg-card">
        <div className="container mx-auto px-4 py-6">
          <div className="flex items-center justify-between">
            <h1 className="text-2xl font-bold tracking-tight">Google Maps Scraper</h1>
            <div className="flex items-center gap-4">
              <Button variant="outline" asChild>
                <a href="/api/docs" target="_blank" rel="noopener noreferrer">
                  <ExternalLink className="h-4 w-4 mr-2" />
                  API Docs
                </a>
              </Button>
              <Button asChild>
                <a
                  href="https://github.com/gosom/google-maps-scraper"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Github className="h-4 w-4 mr-2" />
                  Star on GitHub
                </a>
              </Button>
            </div>
          </div>
        </div>
      </header>

      <main className="container mx-auto px-4 py-8">
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-8">
          <div className="lg:col-span-1">
            <Card>
              <CardHeader>
                <CardTitle>New Scraping Job</CardTitle>
              </CardHeader>
              <CardContent>
                <JobForm onSubmit={handleSubmit} error={error} />
              </CardContent>
            </Card>
          </div>

          <div className="lg:col-span-2">
            <Card>
              <CardHeader>
                <CardTitle>Jobs</CardTitle>
              </CardHeader>
              <CardContent>
                {loading ? (
                  <div className="flex items-center justify-center py-12">
                    <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
                  </div>
                ) : (
                  <JobTable
                    jobs={jobs}
                    onJobDeleted={fetchJobs}
                    onViewJob={handleViewJob}
                  />
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      </main>

      <JobMapModal
        job={selectedJob}
        places={places}
        loading={mapLoading}
        error={mapError}
        onClose={() => setSelectedJob(null)}
      />
    </div>
  )
}

export default App
