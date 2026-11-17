import { DeviceAtTab } from "../devices/DeviceAtTab";
import { DeviceUssdTab } from "../devices/DeviceUssdTab";

interface CardAtUssdTabProps {
  deviceId: string;
  iccid: string;
  backendMode: string;
  atPort: string;
  atRunning: boolean;
  atReader: boolean;
  operable: boolean;
}

export function CardAtUssdTab({
  deviceId,
  iccid,
  backendMode,
  atPort,
  atRunning,
  atReader,
  operable,
}: CardAtUssdTabProps) {
  return (
    <div className="at-ussd-tab-shell">
      <DeviceAtTab
        key={`at:${iccid}`}
        deviceId={deviceId}
        iccid={iccid}
        backendMode={backendMode}
        atPort={atPort}
        running={atRunning}
        reader={atReader}
      />
      <DeviceUssdTab
        key={`ussd:${iccid}`}
        deviceId={deviceId}
        iccid={iccid}
        operable={operable}
      />
    </div>
  );
}
