// Mitto Web Interface - CountdownDisplay Component
// Shared live countdown to a target time, rendered with daisyUI `countdown` spans.
// Granularity adapts to the schedule unit (days/hours/minutes) and the component
// manages its own ticking interval so callers only pass the target + unit.

const { useState, useEffect, html } = window.preact;

/**
 * Compute adaptive countdown segments to the next scheduled run.
 * Granularity adapts to the schedule unit:
 *   - days  -> [days, hours, minutes]
 *   - hours -> [hours, minutes, seconds]
 *   - other -> [minutes, seconds]
 * The countdown component caps at 999, so the leading segment accumulates any
 * larger units (e.g. days fold into hours for an hourly schedule).
 *
 * @param {string} targetIso - ISO timestamp of the next run
 * @param {string} unit - Schedule unit ("minutes" | "hours" | "days")
 * @param {number} nowMs - Current time in ms (Date.now())
 * @returns {Array<{value:number,label:string}>|null} Segments, or null if invalid
 */
export function getCountdownSegments(targetIso, unit, nowMs) {
  if (!targetIso) return null;
  const targetMs = new Date(targetIso).getTime();
  if (Number.isNaN(targetMs)) return null;

  let secs = Math.max(0, Math.floor((targetMs - nowMs) / 1000));
  const days = Math.floor(secs / 86400);
  secs -= days * 86400;
  const hours = Math.floor(secs / 3600);
  secs -= hours * 3600;
  const minutes = Math.floor(secs / 60);
  const seconds = secs - minutes * 60;

  if (unit === "days") {
    return [
      { value: days, label: "d" },
      { value: hours, label: "h" },
      { value: minutes, label: "m" },
    ];
  }
  if (unit === "hours") {
    return [
      { value: days * 24 + hours, label: "h" },
      { value: minutes, label: "m" },
      { value: seconds, label: "s" },
    ];
  }
  return [
    { value: days * 1440 + hours * 60 + minutes, label: "m" },
    { value: seconds, label: "s" },
  ];
}

/**
 * CountdownDisplay - live, adaptive countdown to `targetIso`, rendered with
 * daisyUI `countdown` spans. Manages its own ticking interval: minute/hour
 * schedules tick every second (to show seconds); daily schedules tick every
 * 60s. Renders nothing when there is no valid target.
 *
 * @param {Object} props
 * @param {string} props.targetIso - ISO timestamp of the next run (falsy => renders nothing)
 * @param {string} props.unit - Schedule unit ("minutes" | "hours" | "days")
 * @param {boolean} [props.active=true] - When false, ticking is paused (e.g. panel closed)
 * @param {string} [props.title] - Optional hover tooltip (e.g. absolute next-run time)
 * @param {string} [props.className] - Optional extra classes for the wrapper span
 */
export function CountdownDisplay({
  targetIso,
  unit,
  active = true,
  title = "",
  className = "",
}) {
  // Current time (ms), ticked by an interval to drive the live countdown
  const [nowMs, setNowMs] = useState(() => Date.now());

  // Tick while active and a target is set. Minute/hour schedules show seconds
  // (1s tick); daily schedules tick every 60s to minimize re-renders.
  useEffect(() => {
    if (!active || !targetIso) {
      return;
    }
    const intervalMs = unit === "days" ? 60000 : 1000;
    setNowMs(Date.now());
    const intervalId = setInterval(() => {
      setNowMs(Date.now());
    }, intervalMs);
    return () => clearInterval(intervalId);
  }, [active, targetIso, unit]);

  const segments = getCountdownSegments(targetIso, unit, nowMs);
  if (!segments) return null;

  return html`<span
    class="inline-flex items-baseline gap-1 font-mono ${className}"
    title=${title}
  >
    ${segments.map(
      (seg) =>
        html`<span class="inline-flex items-baseline"
          ><span class="countdown"
            ><span
              style="--value:${seg.value};"
              aria-live="polite"
              aria-label=${String(seg.value)}
              >${seg.value}</span
            ></span
          >${seg.label}</span
        >`,
    )}
  </span>`;
}
