package org.saturday.emulator;

import android.app.Activity;
import android.os.Bundle;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;

import org.saturday.emulator.mobile.EbitenView;

public final class MainActivity extends Activity {
    private EbitenView ebitenView;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        getWindow().setFlags(
            WindowManager.LayoutParams.FLAG_FULLSCREEN,
            WindowManager.LayoutParams.FLAG_FULLSCREEN
        );
        getWindow().getDecorView().setSystemUiVisibility(
            View.SYSTEM_UI_FLAG_FULLSCREEN
                | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
        );

        ebitenView = new EbitenView(this);
        setContentView(ebitenView);
    }

    @Override
    protected void onPause() {
        if (ebitenView != null) {
            ebitenView.suspendGame();
        }
        super.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();
        if (ebitenView != null) {
            ebitenView.resumeGame();
        }
    }
}
