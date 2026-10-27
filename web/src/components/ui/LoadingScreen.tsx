import { BrandLogo } from "../shell/BrandLogo";

export function LoadingScreen() {
  return (
    <div className="vonet-boot-loader boot-loader-v2" role="status" aria-live="polite" aria-label="Loading VoNet">
      <div className="boot-loader-orbit" aria-hidden="true">
        <span /><span /><span />
      </div>
      <BrandLogo className="vonet-boot-logo" />
      <div className="vonet-boot-copy">
        <div className="vonet-boot-title">VoNet</div>
        <div className="vonet-boot-description">MODEM · PC/SC · PANEL</div>
      </div>
      <div className="boot-loader-track" aria-hidden="true"><span /></div>
      <span className="sr-only">Loading VoNet</span>
    </div>
  );
}
