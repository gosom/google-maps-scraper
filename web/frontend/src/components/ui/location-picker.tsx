import { useState, useEffect } from "react"
import { MapContainer, TileLayer, Marker, useMapEvents, useMap } from "react-leaflet"
import L from "leaflet"
import "leaflet/dist/leaflet.css" // eslint-disable-line
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog"

delete (L.Icon.Default.prototype as any)._getIconUrl
L.Icon.Default.mergeOptions({
  iconRetinaUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-icon-2x.png",
  iconUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-icon.png",
  shadowUrl: "https://cdnjs.cloudflare.com/ajax/libs/leaflet/1.9.4/images/marker-shadow.png",
})

interface LocationPickerProps {
  lat: string
  lon: string
  zoom: number
  onLatChange: (value: string) => void
  onLonChange: (value: string) => void
  onZoomChange: (value: number) => void
}

function MapEvents({ onLocationSelect, onZoomChange }: { 
  onLocationSelect: (lat: number, lon: number) => void
  onZoomChange: (zoom: number) => void 
}) {
  useMapEvents({
    click(e) {
      onLocationSelect(e.latlng.lat, e.latlng.lng)
    },
    zoomend(e) {
      const map = e.target as L.Map
      onZoomChange(map.getZoom())
    },
  })
  return null
}

function MapController({ lat, lon, zoom }: { lat: number; lon: number; zoom: number }) {
  const map = useMap()
  
  useEffect(() => {
    if (!isNaN(lat) && !isNaN(lon)) {
      map.setView([lat, lon], zoom)
    }
  }, [lat, lon, zoom, map])
  
  return null
}

export function LocationPicker({ lat, lon, zoom, onLatChange, onLonChange, onZoomChange }: LocationPickerProps) {
  const [open, setOpen] = useState(false)
  const [localLat, setLocalLat] = useState(parseFloat(lat) || 0)
  const [localLon, setLocalLon] = useState(parseFloat(lon) || 0)
  const [localZoom, setLocalZoom] = useState(zoom)

  const handleLocationSelect = (newLat: number, newLon: number) => {
    setLocalLat(newLat)
    setLocalLon(newLon)
  }

  const handleZoomChange = (newZoom: number) => {
    setLocalZoom(newZoom)
  }

  const handleConfirm = () => {
    onLatChange(localLat.toString())
    onLonChange(localLon.toString())
    onZoomChange(localZoom)
    setOpen(false)
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant="outline" type="button" className="w-full">
          📍 Select Location on Map
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-4xl h-[80vh]">
        <DialogHeader>
          <DialogTitle>Select Location</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-4 h-full">
          <div className="flex gap-4 items-end">
            <div className="flex-1">
              <Label>Latitude</Label>
              <Input
                type="number"
                step="0.000001"
                value={localLat.toFixed(6)}
                onChange={(e) => setLocalLat(parseFloat(e.target.value) || 0)}
              />
            </div>
            <div className="flex-1">
              <Label>Longitude</Label>
              <Input
                type="number"
                step="0.000001"
                value={localLon.toFixed(6)}
                onChange={(e) => setLocalLon(parseFloat(e.target.value) || 0)}
              />
            </div>
            <div className="w-24">
              <Label>Zoom</Label>
              <Input
                type="number"
                min={1}
                max={20}
                value={localZoom}
                onChange={(e) => setLocalZoom(parseInt(e.target.value) || 15)}
              />
            </div>
          </div>
          <p className="text-sm text-muted-foreground">
            Click on the map to select a location. The marker shows the selected position.
          </p>
          <div className="flex-1 min-h-[400px] rounded-lg overflow-hidden border">
            <MapContainer
              center={[localLat || 51.505, localLon || -0.09]}
              zoom={localZoom}
              style={{ height: "100%", width: "100%" }}
            >
              <TileLayer
                attribution='&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>'
                url="https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png"
              />
              <Marker position={[localLat, localLon]} />
              <MapEvents onLocationSelect={handleLocationSelect} onZoomChange={handleZoomChange} />
              <MapController lat={localLat} lon={localLon} zoom={localZoom} />
            </MapContainer>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button onClick={handleConfirm}>
              Confirm Location
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
