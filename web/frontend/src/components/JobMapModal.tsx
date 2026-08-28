import { useEffect, useRef } from "react"
import { MapContainer, TileLayer, Marker, Popup, useMap } from "react-leaflet"
import L from "leaflet"
import "leaflet/dist/leaflet.css" // eslint-disable-line
import type { Place } from "@/types"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Loader2 } from "lucide-react"

delete (L.Icon.Default.prototype as any)._getIconUrl
L.Icon.Default.mergeOptions({
  iconRetinaUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-icon-2x.png",
  iconUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-icon.png",
  shadowUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-shadow.png",
})

interface JobMapModalProps {
  job: { id: string; name: string } | null
  places: Place[]
  loading: boolean
  error: string | null
  onClose: () => void
}

function MapBounds({ places }: { places: Place[] }) {
  const map = useMap()

  useEffect(() => {
    if (places.length > 0) {
      const bounds = L.latLngBounds(places.map((p) => [p.latitude, p.longitude]))
      map.fitBounds(bounds, { padding: [50, 50] })
    }
  }, [places, map])

  return null
}

export function JobMapModal({ job, places, loading, error, onClose }: JobMapModalProps) {
  const mapRef = useRef<L.Map | null>(null)

  if (!job) return null

  return (
    <Dialog open={!!job} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-6xl h-[85vh]">
        <DialogHeader>
          <DialogTitle>{job.name} - Results</DialogTitle>
        </DialogHeader>
        <div className="flex gap-4 flex-1 min-h-0">
          <div className="flex-1 rounded-lg overflow-hidden border">
            {loading && (
              <div className="h-full flex items-center justify-center">
                <Loader2 className="h-8 w-8 animate-spin text-muted-foreground" />
              </div>
            )}
            {error && (
              <div className="h-full flex items-center justify-center text-red-500">
                {error}
              </div>
            )}
            {!loading && !error && places.length === 0 && (
              <div className="h-full flex items-center justify-center text-muted-foreground">
                No places found for this job.
              </div>
            )}
            {!loading && !error && places.length > 0 && (
              <MapContainer
                ref={mapRef as any}
                center={[places[0].latitude, places[0].longitude]}
                zoom={13}
                style={{ height: "100%", width: "100%" }}
              >
                <TileLayer
                  attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
                  url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
                />
                <MapBounds places={places} />
                {places.map((place, idx) => (
                  <Marker key={idx} position={[place.latitude, place.longitude]}>
                    <Popup>
                      <div className="space-y-1">
                        <div className="font-semibold">{place.title}</div>
                        <div className="text-sm">{place.address}</div>
                        {place.rating && (
                          <div className="text-sm text-amber-600">
                            ⭐ {place.rating} ({place.reviews} reviews)
                          </div>
                        )}
                        {place.phone && <div className="text-sm">📞 {place.phone}</div>}
                        {place.website && (
                          <a
                            href={place.website}
                            target="_blank"
                            rel="noopener noreferrer"
                            className="text-sm text-blue-600 hover:underline block"
                          >
                            Website
                          </a>
                        )}
                      </div>
                    </Popup>
                  </Marker>
                ))}
              </MapContainer>
            )}
          </div>
          {!loading && !error && places.length > 0 && (
            <ScrollArea className="w-80 border rounded-lg">
              <div className="p-2 space-y-2">
                {places.map((place, idx) => (
                  <div
                    key={idx}
                    className="p-3 rounded-md border hover:bg-muted cursor-pointer transition-colors"
                    onClick={() => {
                      if (mapRef.current) {
                        mapRef.current.setView([place.latitude, place.longitude], 16)
                      }
                    }}
                  >
                    <div className="font-medium text-sm">{place.title}</div>
                    <div className="text-xs text-muted-foreground mt-1">
                      {place.address}
                    </div>
                    {place.rating && (
                      <div className="text-xs text-amber-600 mt-1">
                        ⭐ {place.rating} ({place.reviews})
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </ScrollArea>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
