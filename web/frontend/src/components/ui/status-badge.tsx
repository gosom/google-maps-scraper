import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

interface StatusBadgeProps {
  status: "pending" | "working" | "ok" | "failed"
  className?: string
}

const statusConfig = {
  pending: { label: "Pending", className: "bg-yellow-500 hover:bg-yellow-600" },
  working: { label: "Working", className: "bg-blue-500 hover:bg-blue-600" },
  ok: { label: "Completed", className: "bg-green-500 hover:bg-green-600" },
  failed: { label: "Failed", className: "bg-red-500 hover:bg-red-600" },
}

export function StatusBadge({ status, className }: StatusBadgeProps) {
  const config = statusConfig[status]
  
  return (
    <Badge className={cn(config.className, className)}>
      {config.label}
    </Badge>
  )
}
