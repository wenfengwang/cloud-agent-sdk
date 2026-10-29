"use client";

import { useSearchParams } from "next/navigation";
import { Conversation } from "@/components/conversation";
import { Home } from "@/components/home";

export default function Page() {
  const session = useSearchParams().get("session");
  return session ? <Conversation key={session} sessionId={session} /> : <Home />;
}
