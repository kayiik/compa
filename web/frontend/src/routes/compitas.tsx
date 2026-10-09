import { createFileRoute } from "@tanstack/react-router"

import { CompitasPage } from "@/components/compitas/compitas-page"

export const Route = createFileRoute("/compitas")({
  component: CompitasPage,
})
