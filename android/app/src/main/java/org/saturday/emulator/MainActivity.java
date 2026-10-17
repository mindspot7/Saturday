package org.saturday.emulator;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.os.Handler;
import android.os.Looper;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.View;
import android.view.Window;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.TextView;

import androidx.documentfile.provider.DocumentFile;

import org.saturday.emulator.mobile.EbitenView;
import org.saturday.emulator.mobile.Mobile;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;
import java.util.ArrayList;
import java.util.List;

public final class MainActivity extends Activity {
    private static final int REQUEST_GAME_FOLDER = 101;
    private static final int REQUEST_BIOS = 102;

    private EbitenView ebitenView;
    private TextView statusView;
    private File gameDirectory;
    private boolean gameRunning;
    private final Handler uiHandler = new Handler(Looper.getMainLooper());
    private final Runnable diagnosticsUpdater = new Runnable() {
        @Override public void run() {
            if (gameRunning && statusView != null) {
                statusView.setText(Mobile.getDiagnostics());
                uiHandler.postDelayed(this, 1000);
            }
        }
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        requestWindowFeature(Window.FEATURE_NO_TITLE);
        getWindow().setFlags(WindowManager.LayoutParams.FLAG_FULLSCREEN,
                WindowManager.LayoutParams.FLAG_FULLSCREEN);
        hideSystemUi();

        FrameLayout root = new FrameLayout(this);
        ebitenView = new EbitenView(this);
        root.addView(ebitenView, new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));

        LinearLayout panel = new LinearLayout(this);
        panel.setOrientation(LinearLayout.VERTICAL);
        panel.setPadding(dp(10), dp(8), dp(10), dp(8));
        panel.setBackgroundColor(0xCC101722);

        LinearLayout actions = new LinearLayout(this);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        Button gamesButton = new Button(this);
        gamesButton.setText("Choose game folder");
        gamesButton.setOnClickListener(v -> chooseGameFolder());
        actions.addView(gamesButton, new LinearLayout.LayoutParams(0, dp(48), 1));

        Button biosButton = new Button(this);
        biosButton.setText("Optional BIOS");
        biosButton.setOnClickListener(v -> chooseBios());
        actions.addView(biosButton, new LinearLayout.LayoutParams(0, dp(48), 1));
        panel.addView(actions);

        statusView = new TextView(this);
        statusView.setTextColor(0xFFFFFFFF);
        statusView.setTextSize(13);
        statusView.setPadding(dp(4), dp(4), dp(4), dp(2));
        statusView.setText("SATURDAY · SEGA SATURN\n\nBuilt-in HLE BIOS is enabled automatically. An external BIOS is optional.\n\nChoose a folder containing CHD or CUE/BIN files.");
        panel.addView(statusView);

        FrameLayout.LayoutParams panelParams = new FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.WRAP_CONTENT);
        panelParams.gravity = android.view.Gravity.TOP;
        root.addView(panel, panelParams);
        setContentView(root);
    }

    private void hideSystemUi() {
        getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_FULLSCREEN
                        | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                        | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                        | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                        | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN);
    }

    private int dp(int value) {
        return (int) (value * getResources().getDisplayMetrics().density + 0.5f);
    }

    private void chooseGameFolder() {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT_TREE);
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION
                | Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION);
        startActivityForResult(intent, REQUEST_GAME_FOLDER);
    }

    private void chooseBios() {
        Intent intent = new Intent(Intent.ACTION_OPEN_DOCUMENT);
        intent.setType("*/*");
        intent.addCategory(Intent.CATEGORY_OPENABLE);
        intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION);
        startActivityForResult(intent, REQUEST_BIOS);
    }

    @Override
    protected void onActivityResult(int requestCode, int resultCode, Intent data) {
        super.onActivityResult(requestCode, resultCode, data);
        if (resultCode != RESULT_OK || data == null || data.getData() == null) return;

        Uri uri = data.getData();
        try {
            if (requestCode == REQUEST_GAME_FOLDER) {
                getContentResolver().takePersistableUriPermission(uri,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION);
                importGameFolder(uri);
            } else if (requestCode == REQUEST_BIOS) {
                File bios = new File(getCacheDir(), "saturn-bios.bin");
                copyUri(uri, bios);
                String result = Mobile.loadBIOS(bios.getAbsolutePath());
                statusView.setText(result == null || result.isEmpty()
                        ? "BIOS loaded. " + Mobile.getStatus() : result);
            }
        } catch (Exception e) {
            statusView.setText("Unable to open selected content: " + e.getMessage());
        }
    }

    private void importGameFolder(Uri uri) throws Exception {
        DocumentFile folder = DocumentFile.fromTreeUri(this, uri);
        if (folder == null || !folder.isDirectory()) {
            statusView.setText("Please select a folder.");
            return;
        }

        String folderName = folder.getName();
        if (folderName == null || folderName.trim().isEmpty()) folderName = "games";
        gameDirectory = new File(getCacheDir(), "games/" + folderName.replaceAll("[^a-zA-Z0-9._-]", "_"));
        if (!gameDirectory.exists() && !gameDirectory.mkdirs()) {
            throw new IllegalStateException("Could not create game cache directory");
        }

        List<File> discs = new ArrayList<>();
        for (DocumentFile entry : folder.listFiles()) {
            if (!entry.isFile() || entry.getName() == null) continue;
            String name = entry.getName();
            String lower = name.toLowerCase(java.util.Locale.ROOT);
            File destination = new File(gameDirectory, name);
            copyUri(entry.getUri(), destination);
            if (lower.endsWith(".chd") || lower.endsWith(".cue")) discs.add(destination);
        }

        if (discs.isEmpty()) {
            statusView.setText("No .CHD or .CUE files found in this folder.");
            return;
        }
        String[] labels = new String[discs.size()];
        for (int i = 0; i < discs.size(); i++) labels[i] = discs.get(i).getName();
        new AlertDialog.Builder(this)
                .setTitle("Select Saturn disc")
                .setItems(labels, (dialog, which) -> {
                    String result = Mobile.loadDisc(discs.get(which).getAbsolutePath());
                    gameRunning = result == null || result.isEmpty();
                    statusView.setText(gameRunning ? Mobile.getDiagnostics() : result);
                    if (gameRunning) uiHandler.postDelayed(diagnosticsUpdater, 1000);
                })
                .setNegativeButton("Cancel", null)
                .show();
    }

    private void copyUri(Uri uri, File destination) throws Exception {
        try (InputStream in = getContentResolver().openInputStream(uri);
             FileOutputStream out = new FileOutputStream(destination)) {
            if (in == null) throw new IllegalStateException("Could not read selected file");
            byte[] buffer = new byte[64 * 1024];
            int count;
            while ((count = in.read(buffer)) != -1) out.write(buffer, 0, count);
        }
    }

    @Override
    public boolean dispatchKeyEvent(KeyEvent event) {
        int button = saturnButtonForKey(event.getKeyCode());
        if (button >= 0) {
            Mobile.setButton(button, event.getAction() == KeyEvent.ACTION_DOWN);
            return true;
        }
        return super.dispatchKeyEvent(event);
    }

    @Override
    public boolean onGenericMotionEvent(MotionEvent event) {
        if ((event.getSource() & android.view.InputDevice.SOURCE_JOYSTICK)
                == android.view.InputDevice.SOURCE_JOYSTICK
                && event.getAction() == MotionEvent.ACTION_MOVE) {
            float x = event.getAxisValue(MotionEvent.AXIS_X);
            float y = event.getAxisValue(MotionEvent.AXIS_Y);
            Mobile.setButton(2, x < -0.45f);
            Mobile.setButton(3, x > 0.45f);
            Mobile.setButton(0, y < -0.45f);
            Mobile.setButton(1, y > 0.45f);
            return true;
        }
        return super.onGenericMotionEvent(event);
    }

    private int saturnButtonForKey(int keyCode) {
        switch (keyCode) {
            case KeyEvent.KEYCODE_DPAD_UP: return 0;
            case KeyEvent.KEYCODE_DPAD_DOWN: return 1;
            case KeyEvent.KEYCODE_DPAD_LEFT: return 2;
            case KeyEvent.KEYCODE_DPAD_RIGHT: return 3;
            // Standard Android gamepad face buttons.
            case KeyEvent.KEYCODE_BUTTON_A: return 4; // Saturn A
            case KeyEvent.KEYCODE_BUTTON_B: return 5; // Saturn B
            case KeyEvent.KEYCODE_BUTTON_X: return 7; // Saturn X
            case KeyEvent.KEYCODE_BUTTON_Y: return 8; // Saturn Y
            // Extra numbered buttons cover six-button controllers that
            // expose their additional face buttons through Android's generic IDs.
            case KeyEvent.KEYCODE_BUTTON_1: return 6; // Saturn C
            case KeyEvent.KEYCODE_BUTTON_2: return 9; // Saturn Z
            case KeyEvent.KEYCODE_BUTTON_L1:
            case KeyEvent.KEYCODE_BUTTON_L2: return 10; // Saturn L
            case KeyEvent.KEYCODE_BUTTON_R1:
            case KeyEvent.KEYCODE_BUTTON_R2: return 11; // Saturn R
            case KeyEvent.KEYCODE_BUTTON_START:
            case KeyEvent.KEYCODE_ENTER: return 12;
            default: return -1;
        }
    }

    @Override
    protected void onPause() {
        uiHandler.removeCallbacks(diagnosticsUpdater);
        if (ebitenView != null) ebitenView.suspendGame();
        super.onPause();
    }

    @Override
    protected void onResume() {
        super.onResume();
        hideSystemUi();
        if (ebitenView != null) ebitenView.resumeGame();
        if (gameRunning) uiHandler.postDelayed(diagnosticsUpdater, 1000);
    }
}
