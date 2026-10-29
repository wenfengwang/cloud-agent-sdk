"use client";

import { Slot } from "radix-ui";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useApp } from "@/lib/state/app";
import { cn } from "@/lib/utils";

/**
 * A control the SDK cannot support: kept in place, dimmed, focusable, and
 * inert, with the gap's reason as its tooltip.
 */
export function GapControl({
  gap,
  children,
  className,
}: {
  gap: string;
  children: React.ReactElement;
  className?: string;
}) {
  const { gapReason } = useApp();
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Slot.Root
          aria-disabled="true"
          data-gap={gap}
          tabIndex={0}
          className={cn("cursor-not-allowed opacity-45", className)}
          onClickCapture={(e: React.MouseEvent) => {
            e.preventDefault();
            e.stopPropagation();
          }}
          onKeyDownCapture={(e: React.KeyboardEvent) => {
            if (e.key === "Enter" || e.key === " ") e.preventDefault();
          }}
        >
          {children}
        </Slot.Root>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">Not available: {gapReason(gap)}</TooltipContent>
    </Tooltip>
  );
}
