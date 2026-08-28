import { useState } from "react"
import type { CreateJobRequest } from "@/types"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { TagsInput } from "@/components/ui/tags-input"
import { LocationPicker } from "@/components/ui/location-picker"
import { Loader2 } from "lucide-react"

interface JobFormProps {
  onSubmit: (job: CreateJobRequest) => Promise<void>
  error: string | null
}

export function JobForm({ onSubmit, error }: JobFormProps) {
  const [name, setName] = useState("")
  const [keywords, setKeywords] = useState<string[]>([])
  const [lang, setLang] = useState("en")
  const [zoom, setZoom] = useState(15)
  const [lat, setLat] = useState("")
  const [lon, setLon] = useState("")
  const [fastMode, setFastMode] = useState(false)
  const [radius, setRadius] = useState(10000)
  const [depth, setDepth] = useState(10)
  const [email, setEmail] = useState(false)
  const [maxTime, setMaxTime] = useState(600)
  const [proxies, setProxies] = useState("")
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setLoading(true)

    try {
      await onSubmit({
        name,
        keywords,
        lang,
        zoom,
        lat,
        lon,
        fast_mode: fastMode,
        radius,
        depth,
        email,
        extra_reviews: false,
        max_time: maxTime,
        proxies: proxies.split("\n").map((p) => p.trim()).filter(Boolean),
      })
      setName("")
      setKeywords([])
      setProxies("")
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6">
      {error && (
        <div className="p-3 text-sm text-red-500 bg-red-50 border border-red-200 rounded-md">
          {error}
        </div>
      )}

      <Card>
        <CardHeader>
          <CardTitle>Job Details</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="name">Job Name</Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="My Scraping Job"
              required
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="keywords">Keywords</Label>
            <TagsInput
              value={keywords}
              onChange={setKeywords}
              placeholder="Type and press Enter to add keywords..."
            />
            <p className="text-xs text-muted-foreground">
              Press Enter or comma to add a keyword
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="lang">Language Code</Label>
            <Input
              id="lang"
              value={lang}
              onChange={(e) => setLang(e.target.value)}
              maxLength={2}
              placeholder="en"
              required
              className="w-20"
            />
          </div>
        </CardContent>
      </Card>

      <Accordion type="single" collapsible className="w-full">
        <AccordionItem value="location">
          <AccordionTrigger>Location Settings</AccordionTrigger>
          <AccordionContent>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label>Select Location</Label>
                <LocationPicker
                  lat={lat}
                  lon={lon}
                  zoom={zoom}
                  onLatChange={setLat}
                  onLonChange={setLon}
                  onZoomChange={setZoom}
                />
                <div className="grid grid-cols-3 gap-2">
                  <div>
                    <Label className="text-xs">Lat</Label>
                    <Input
                      type="number"
                      step="0.000001"
                      value={lat}
                      onChange={(e) => setLat(e.target.value)}
                      placeholder="0.0"
                      className="h-8"
                    />
                  </div>
                  <div>
                    <Label className="text-xs">Lon</Label>
                    <Input
                      type="number"
                      step="0.000001"
                      value={lon}
                      onChange={(e) => setLon(e.target.value)}
                      placeholder="0.0"
                      className="h-8"
                    />
                  </div>
                  <div>
                    <Label className="text-xs">Zoom</Label>
                    <Input
                      type="number"
                      min={1}
                      max={20}
                      value={zoom}
                      onChange={(e) => setZoom(parseInt(e.target.value) || 15)}
                      className="h-8"
                    />
                  </div>
                </div>
              </div>
            </div>
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="advanced">
          <AccordionTrigger>Advanced Options</AccordionTrigger>
          <AccordionContent>
            <div className="space-y-4 pt-4">
              <div className="flex items-center justify-between">
                <div className="space-y-0.5">
                  <Label>Fast Mode (BETA)</Label>
                  <p className="text-xs text-muted-foreground">
                    Requires location coordinates
                  </p>
                </div>
                <Switch checked={fastMode} onCheckedChange={setFastMode} />
              </div>
              <div className="grid grid-cols-2 gap-4">
                <div className="space-y-2">
                  <Label htmlFor="radius">Radius (meters)</Label>
                  <Input
                    id="radius"
                    type="number"
                    value={radius}
                    onChange={(e) => setRadius(Number(e.target.value))}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="depth">Depth</Label>
                  <Input
                    id="depth"
                    type="number"
                    value={depth}
                    onChange={(e) => setDepth(Number(e.target.value))}
                  />
                </div>
              </div>
              <div className="flex items-center justify-between">
                <div className="space-y-0.5">
                  <Label>Fetch Emails</Label>
                  <p className="text-xs text-muted-foreground">
                    Extract email addresses from websites
                  </p>
                </div>
                <Switch checked={email} onCheckedChange={setEmail} />
              </div>
              <div className="space-y-2">
                <Label htmlFor="maxtime">Max Duration (seconds)</Label>
                <Input
                  id="maxtime"
                  type="number"
                  value={maxTime}
                  onChange={(e) => setMaxTime(Number(e.target.value))}
                />
              </div>
            </div>
          </AccordionContent>
        </AccordionItem>

        <AccordionItem value="proxies">
          <AccordionTrigger>Proxies</AccordionTrigger>
          <AccordionContent>
            <div className="space-y-4 pt-4">
              <div className="space-y-2">
                <Label htmlFor="proxies">Proxy List (one per line)</Label>
                <Textarea
                  id="proxies"
                  rows={5}
                  value={proxies}
                  onChange={(e) => setProxies(e.target.value)}
                  placeholder="https://user:pass@proxy.com:443&#10;socks5://127.0.0.1:8000"
                />
                <p className="text-xs text-muted-foreground">
                  Supports HTTP, HTTPS, and SOCKS5 proxies
                </p>
              </div>
            </div>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

      <Button type="submit" className="w-full" disabled={loading || keywords.length === 0}>
        {loading && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
        {loading ? "Starting..." : "Start Scraping"}
      </Button>
    </form>
  )
}
