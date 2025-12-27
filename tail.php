<?php
    require_once('../admin/dl.inc.php');

    if( ( $f_tail = $_GET['tail'] ) ) {
        $t_root = realpath( get_bruno_home() . '/data/mai/log' );
        $t_file = realpath( $t_root . '/' . $f_tail );
        if( !$t_file ) {
            errdie( "$t_file not found", '404 Not Found' );
        } else if ( $t_root != substr( $t_file, 0, len($t_root) ) ) {
            errdie( "$t_file is not under $t_root", '401 Access Denied' );
        }
        $t_cmd = 'tail -F ' . escapeshellarg( $t_file ) . ' 2>&1';
        $t_pipe = popen( $t_cmd, 'r' );
        if( !$t_pipe ) {
            errdie( "$t_cmd: " . fgets( $t_pipe ) );
        }
        $f_left = $_GET['left'];
        $f_right = $_GET['right'];

        // https://kevinchoppin.dev/blog/server-sent-events-in-php
        // make session read-only
        session_start();
        session_write_close();

        // disable default disconnect checks
        ignore_user_abort(true);

        // set headers for stream
        header("Content-Type: text/event-stream");
        header("Cache-Control: no-cache");
        header("Connection: keep-alive");
        header("Access-Control-Allow-Origin: *");

        // start stream
        while( true ) {
            if( connection_aborted() ) {
                pclose( $t_pipe );
                exit();
            } 

            $t_line = fgets( $t_pipe );
            if( $f_left || $f_right ) {
                echo "data: $t_left" . htmlspecialchars( $t_line ) . "$t_right\n\n";
            } else {
                echo "data: $t_line\n\n";
            }
            ob_flush();
            flush();
        }
        pclose( $t_pipe );

    } elseif( ( $f_file = $_GET['file'] ) ) {

        header('Content-Type: text/html');
        ?><!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>

        <script src="https://unpkg.com/htmx.org@2.0.1" integrity="sha384-QWGpdj554B4ETpJJC9z+ZHJcA/i59TyjxEPXiiUgN2WmTyV5OEZWCD6gQhgkdpB/" crossorigin="anonymous"></script>
        <script src="https://unpkg.com/htmx-ext-sse@2.2.1/sse.js"></script>
    </head>
    <body>
        <h1><?php echo htmlspecialchars( $f_file ); ?></h1>
        <pre hx-ext="sse" sse-connect="?left=&right=<br>&tail=<?php echo urlencode( $f_file ); ?>" sse-swap="message" hx-swap="afterbegin swap:1s">
        </pre>
    </body>
</html><?php

    } else {

        $f_dir = $_GET['dir'];
        $t_dir = realpath( $t_root . '/' . $f_dir );
        if( !$t_dir ) {
            errdie( "$t_dir not found", '404 Not Found' );
        } else if ( $t_root != substr( $t_dir, 0, len($t_root) ) ) {
            errdie( "$t_dir is not under $t_root", '401 Access Denied' );
        }
        if( !( $t_handle = opendir( $t_dir ) ) ) {
            errdie( "cannot open $t_dir", '500 Internal Server Serror' );
        }

        header('Content-Type: text/html');
        ?><!DOCTYPE html>
<html>
    <head>
        <title>WebTail</title>
    </head>
<body>
    <p><ul>
        <?php while( false !== ( $t_bn = readdir( $t_handle ) ) ) {
            $t_fn = $t_dir . '/' . $t_bn;
            $t_typ = '';
            if( is_dir( $t_fn ) ) {
                $t_typ = 'dir';
            } elseif( is_file( $t_fn ) ) {
                $t_typ = 'tail';
            } else {
                continue;
            }
            echo "<li><a href=\"?$t_typ=" . urlencode( $t_fn ) . '">' . htmlspecialchars( $t_bn ) . "</a></li>\n";
        }
        closedir( $t_handle );
        ?>
    </ul></p>
</body>
</html><?php
    }
