import { BrandLogo } from "../shell/BrandLogo";

export function LoadingScreen() {
  return (
    <div className="vonet-boot-loader" role="status" aria-live="polite" aria-label="Loading VoNet">
      <BrandLogo className="vonet-boot-logo" />
      <div className="vonet-boot-copy">
        <div className="vonet-boot-title">VoNet</div>
        <div className="vonet-boot-description">MODEM · PC/SC · PANEL</div>
      </div>
      <span className="sr-only">Loading VoNet</span>
    </div>
  );
}
